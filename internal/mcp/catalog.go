package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abdul-hamid-achik/mcphub/internal/config"
)

const contextualRoutingContractVersion = 1

// toolMatch is the compact, routing-oriented representation of one downstream
// tool. Server metadata is deliberately included: in lazy mode it is often the
// only vocabulary that connects a user's task to a narrowly named tool.
type toolMatch struct {
	Namespaced        string   `json:"namespaced"`
	Server            string   `json:"server"`
	Tool              string   `json:"tool"`
	Title             string   `json:"title,omitempty"`
	Description       string   `json:"description"`
	ServerDescription string   `json:"server_description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	UseWhen           []string `json:"use_when,omitempty"`
	ToolUseWhen       []string `json:"tool_use_when,omitempty"`
	InputFields       []string `json:"-"`
	Score             int      `json:"score,omitempty"`
	MatchedTerms      []string `json:"matched_terms,omitempty"`
	MetadataTruncated bool     `json:"metadata_truncated,omitempty"`
}

const (
	maxDiscoveryQueryBytes = 2048
	maxRoutingIndexBytes   = 4096
	maxIntentTerms         = 64
	maxCatalogMatchesBytes = 12 * 1024
	maxMatchDescription    = 256
	maxMatchTitle          = 160
	maxMatchServerDesc     = 160
	maxMatchTagsBytes      = 128
	maxMatchUseWhenBytes   = 256
	maxMatchTermsBytes     = 256
	maxMatchListItemBytes  = 128
	maxMatchTags           = 6
	maxMatchUseWhen        = 3
	maxMatchTerms          = 24
)

// catalog returns every connected, in-scope downstream tool enriched with the
// server-level vocabulary configured in mcphub.yaml.
func (s *Server) catalog() []toolMatch {
	var entries []toolMatch
	for _, d := range s.hub.Downstreams() {
		if !d.Connected() || !s.scope.allowsServer(d.Name) {
			continue
		}
		srv := s.cfg.Servers[d.Name]
		for _, tool := range d.Tools {
			namespaced := d.Name + "__" + tool.Name
			if !s.scope.allowsNS(namespaced) {
				continue
			}
			title := tool.Title
			if title == "" && tool.Annotations != nil {
				title = tool.Annotations.Title
			}
			_, argumentTemplate, _ := summarizeInputSchema(tool.InputSchema)
			inputFields := make([]string, 0, len(argumentTemplate))
			for name := range argumentTemplate {
				inputFields = append(inputFields, name)
			}
			sort.Strings(inputFields)
			entries = append(entries, toolMatch{
				Namespaced:        namespaced,
				Server:            d.Name,
				Tool:              tool.Name,
				Title:             title,
				Description:       tool.Description,
				ServerDescription: srv.Description,
				Tags:              srv.Tags,
				UseWhen:           srv.UseWhen,
				ToolUseWhen:       srv.ToolUseWhen[tool.Name],
				InputFields:       inputFields,
			})
		}
	}
	return entries
}

// rankCatalog performs deterministic intent-aware lexical ranking. It avoids
// an embedding dependency while fixing the important failure mode of substring
// search: a full natural-language task rarely appears verbatim in tool docs.
func rankCatalog(query string, entries []toolMatch) []toolMatch {
	terms := intentTerms(query)
	if len(terms) == 0 {
		out := append([]toolMatch(nil), entries...)
		sort.Slice(out, func(i, j int) bool { return out[i].Namespaced < out[j].Namespaced })
		return out
	}

	queryText := normalizeText(clipUTF8(query, maxDiscoveryQueryBytes))
	ranked := make([]toolMatch, 0, len(entries))
	for _, entry := range entries {
		fields := []weightedTerms{
			{terms: intentTermSet(entry.Tool), weight: 14},
			{terms: intentTermSet(entry.Server), weight: 12},
			{terms: intentTermSet(entry.Title), weight: 11},
			{terms: intentTermSet(strings.Join(entry.Tags, " ")), weight: 10},
			{terms: intentTermSet(strings.Join(entry.InputFields, " ")), weight: 9},
			{terms: intentTermSet(strings.Join(entry.ToolUseWhen, " ")), weight: 13},
			{terms: intentTermSet(strings.Join(entry.UseWhen, " ")), weight: 9},
			{terms: intentTermSet(entry.Description), weight: 7},
			{terms: intentTermSet(entry.ServerDescription), weight: 5},
		}
		matched := make(map[string]bool, len(terms))
		score := 0
		for _, term := range terms {
			for _, field := range fields {
				if field.terms[term] {
					score += field.weight
					matched[term] = true
					continue
				}
				if termPrefixMatch(term, field.terms) {
					score += max(1, field.weight/3)
					matched[term] = true
				}
			}
		}

		// Preserve the useful precision of the old substring resolver as a
		// bonus, while token coverage remains the primary path.
		if queryText != "" {
			switch {
			case queryText == normalizeText(entry.Tool), queryText == normalizeText(entry.Namespaced):
				score += 40
			case queryText == normalizeText(clipUTF8(entry.Title, maxRoutingIndexBytes)):
				score += 30
			case strings.Contains(normalizeText(entry.Tool), queryText):
				score += 22
			case strings.Contains(normalizeText(clipUTF8(entry.Title, maxRoutingIndexBytes)), queryText):
				score += 18
			case strings.Contains(normalizeText(clipUTF8(entry.Description, maxRoutingIndexBytes)), queryText),
				strings.Contains(normalizeText(clipUTF8(strings.Join(entry.UseWhen, " "), maxRoutingIndexBytes)), queryText):
				score += 12
			}
		}
		if len(matched) == 0 {
			continue
		}
		if len(matched) == len(terms) {
			score += 15
		}
		score += len(matched) * 2
		entry.Score = score
		for _, term := range terms {
			if matched[term] {
				entry.MatchedTerms = append(entry.MatchedTerms, term)
			}
		}
		ranked = append(ranked, entry)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Namespaced < ranked[j].Namespaced
		}
		return ranked[i].Score > ranked[j].Score
	})
	return ranked
}

type routeAssessment struct {
	Status          string   `json:"status"`
	Confidence      string   `json:"confidence"`
	ReasonCodes     []string `json:"reason_codes"`
	MatchedFraction float64  `json:"matched_fraction"`
	ScoreGap        int      `json:"score_gap"`
}

// assessRoute turns lexical ranking into an explicit routing decision. A
// positive token hit is not automatically a clear recommendation: weak
// coverage and near-ties remain ambiguous so callers can search or ask instead
// of silently invoking a coincidental match.
func assessRoute(query string, matches []toolMatch) routeAssessment {
	if len(matches) == 0 {
		return routeAssessment{
			Status:      "no_match",
			Confidence:  "none",
			ReasonCodes: []string{"no_lexical_overlap"},
		}
	}
	terms := intentTerms(query)
	top := matches[0]
	assessment := routeAssessment{Status: "confident", Confidence: "medium"}
	if len(terms) > 0 {
		assessment.MatchedFraction = float64(len(top.MatchedTerms)) / float64(len(terms))
	}
	if len(matches) == 1 {
		assessment.ScoreGap = top.Score
		assessment.ReasonCodes = append(assessment.ReasonCodes, "single_candidate")
	} else {
		assessment.ScoreGap = top.Score - matches[1].Score
	}

	weakCoverage := top.Score < 10 || (len(terms) >= 4 && len(top.MatchedTerms) < 2)
	closeScores := len(matches) > 1 && assessment.ScoreGap <= max(3, top.Score/10)
	scoreTie := len(matches) > 1 && assessment.ScoreGap == 0
	if scoreTie {
		assessment.ReasonCodes = append(assessment.ReasonCodes, "score_tie")
	} else if closeScores {
		assessment.ReasonCodes = append(assessment.ReasonCodes, "close_scores")
	}
	if weakCoverage {
		assessment.ReasonCodes = append(assessment.ReasonCodes, "weak_coverage")
	}
	if scoreTie || closeScores || weakCoverage {
		assessment.Status = "ambiguous"
		assessment.Confidence = "low"
		return assessment
	}
	if assessment.MatchedFraction >= 0.6 && (len(matches) == 1 || assessment.ScoreGap >= max(8, top.Score/4)) {
		assessment.Confidence = "high"
		assessment.ReasonCodes = append(assessment.ReasonCodes, "strong_coverage_and_margin")
	} else {
		assessment.ReasonCodes = append(assessment.ReasonCodes, "clear_margin")
	}
	return assessment
}

// catalogRevision is a stable, content-addressed revision of the live routing
// catalog. It contains no raw metadata itself and changes when connected tools,
// their bounded routing vocabulary, or their top-level input fields change.
func catalogRevision(entries []toolMatch) string {
	ordered := append([]toolMatch(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Namespaced < ordered[j].Namespaced })
	hash := sha256.New()
	write := func(value string) {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	for _, entry := range ordered {
		write(entry.Namespaced)
		write(entry.Title)
		write(entry.Description)
		write(entry.ServerDescription)
		for _, value := range entry.Tags {
			write(value)
		}
		for _, value := range entry.UseWhen {
			write(value)
		}
		for _, value := range entry.ToolUseWhen {
			write(value)
		}
		for _, value := range entry.InputFields {
			write(value)
		}
	}
	sum := hash.Sum(nil)
	return fmt.Sprintf("catalog-v1-%x", sum[:12])
}

type weightedTerms struct {
	terms  map[string]bool
	weight int
}

func intentTermSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, term := range intentTerms(clipUTF8(text, maxRoutingIndexBytes)) {
		set[term] = true
	}
	return set
}

func intentTerms(text string) []string {
	normalized := normalizeText(clipUTF8(text, maxRoutingIndexBytes))
	seen := map[string]bool{}
	var terms []string
	for _, token := range strings.FieldsFunc(normalized, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		term := canonicalIntentTerm(token)
		if stopWords[token] || stopWords[term] || len(term) < 2 || seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
		if len(terms) == maxIntentTerms {
			break
		}
	}
	return terms
}

func compactToolMatch(entry toolMatch) toolMatch {
	out := entry
	var truncated bool
	out.Title, truncated = compactRoutingText(entry.Title, maxMatchTitle)
	out.MetadataTruncated = truncated
	out.Description, truncated = compactRoutingText(entry.Description, maxMatchDescription)
	out.MetadataTruncated = out.MetadataTruncated || truncated
	out.ServerDescription, truncated = compactRoutingText(entry.ServerDescription, maxMatchServerDesc)
	out.MetadataTruncated = out.MetadataTruncated || truncated
	out.Tags, truncated = compactRoutingList(entry.Tags, maxMatchTags, maxMatchTagsBytes)
	out.MetadataTruncated = out.MetadataTruncated || truncated
	out.UseWhen, truncated = compactRoutingList(entry.UseWhen, maxMatchUseWhen, maxMatchUseWhenBytes)
	out.MetadataTruncated = out.MetadataTruncated || truncated
	out.ToolUseWhen, truncated = compactRoutingList(entry.ToolUseWhen, maxMatchUseWhen, maxMatchUseWhenBytes)
	out.MetadataTruncated = out.MetadataTruncated || truncated
	out.MatchedTerms, truncated = compactRoutingList(entry.MatchedTerms, maxMatchTerms, maxMatchTermsBytes)
	out.MetadataTruncated = out.MetadataTruncated || truncated
	return out
}

func compactRoutingText(value string, maxBytes int) (string, bool) {
	compact := strings.Join(strings.Fields(value), " ")
	clipped := clipUTF8(compact, maxBytes)
	return clipped, clipped != compact
}

func compactRoutingList(values []string, maxItems, maxBytes int) ([]string, bool) {
	if len(values) == 0 {
		return nil, false
	}
	out := make([]string, 0, min(len(values), maxItems))
	used := 0
	truncated := false
	for _, value := range values {
		if len(out) == maxItems || used >= maxBytes {
			truncated = true
			break
		}
		compact, clipped := compactRoutingText(value, maxMatchListItemBytes)
		if compact == "" {
			truncated = truncated || value != ""
			continue
		}
		remaining := maxBytes - used
		if len(compact) > remaining {
			compact = clipUTF8(compact, remaining)
			clipped = true
		}
		if compact == "" {
			truncated = true
			break
		}
		out = append(out, compact)
		used += len(compact)
		truncated = truncated || clipped
	}
	if len(out) < len(values) {
		truncated = true
	}
	return out, truncated
}

func compactToolMatches(matches []toolMatch, maxHits int) ([]toolMatch, bool) {
	limit := min(len(matches), maxHits)
	out := make([]toolMatch, 0, limit)
	used := 0
	byteLimited := false
	for _, match := range matches[:limit] {
		compact := compactToolMatch(match)
		encoded, err := json.Marshal(compact)
		if err != nil {
			continue
		}
		cost := len(encoded)
		if len(out) > 0 {
			cost++ // comma in the enclosing JSON array
		}
		if len(out) > 0 && used+cost > maxCatalogMatchesBytes {
			byteLimited = true
			break
		}
		out = append(out, compact)
		used += cost
	}
	return out, byteLimited
}

func discoveryQueryError(query string) string {
	if len(query) > maxDiscoveryQueryBytes {
		return fmt.Sprintf("query exceeds %d bytes", maxDiscoveryQueryBytes)
	}
	return ""
}

func normalizeText(text string) string {
	return strings.Map(func(r rune) rune {
		r = unicode.ToLower(r)
		switch r {
		case 'á', 'à', 'ä', 'â', 'ã':
			return 'a'
		case 'é', 'è', 'ë', 'ê':
			return 'e'
		case 'í', 'ì', 'ï', 'î':
			return 'i'
		case 'ó', 'ò', 'ö', 'ô', 'õ':
			return 'o'
		case 'ú', 'ù', 'ü', 'û':
			return 'u'
		case 'ñ':
			return 'n'
		default:
			return r
		}
	}, text)
}

// canonicalIntentTerm supplies a small bilingual bridge for the task language
// agents commonly use. Server-specific vocabulary still belongs in use_when;
// these aliases only normalize broad workflow concepts.
func canonicalIntentTerm(term string) string {
	switch term {
	case "url", "website", "webpage", "page", "pagina", "paginas", "sitio", "internet", "http", "fetch", "download", "descargar", "bajar", "scrape", "crawl", "capture", "capturar":
		return "web"
	case "md":
		return "markdown"
	case "research", "investigate", "investigation", "investigating", "investigar", "investigando", "analyze", "analysis", "analizar", "evidence", "evidencia":
		return "research"
	case "source", "sources", "fuente", "fuentes":
		return "source"
	case "build", "builder", "construct", "construction", "create", "develop", "developing", "development", "implement", "implementation", "feature", "crear", "construir", "construccion", "desarrollar", "desarrollo", "implementar", "codigo":
		return "build"
	case "plan", "planning", "planear", "planificar", "planificacion":
		return "plan"
	case "verify", "verification", "validate", "validation", "verificar", "verificacion", "validar", "validacion":
		return "verify"
	case "database", "databases", "db", "sql":
		return "database"
	case "command", "commands", "terminal", "shell", "comando", "comandos":
		return "cli"
	case "file", "files", "archivo", "archivos", "artifact", "artifacts", "document", "documents", "save", "saved", "saving", "stored", "storage", "persist", "persisted", "persistence", "stash", "guardar", "guardado", "guardados", "almacenar", "almacenado":
		return "file"
	case "video", "videos", "mp4", "mov", "mkv", "webm", "recording", "grabacion", "grabaciones":
		return "video"
	case "image", "images", "photo", "photos", "picture", "pictures", "screenshot", "screenshots", "png", "jpg", "jpeg", "webp", "imagen", "imagenes", "foto", "fotos", "captura", "capturas":
		return "image"
	case "audio", "mp3", "wav", "m4a", "sound", "speech", "sonido", "voz":
		return "audio"
	case "read", "reading", "retrieve", "retrieval", "inspect", "inspection", "get", "info", "information", "detail", "details", "show", "preview", "view", "leer", "recuperar", "obtener", "consultar", "inspeccionar", "mostrar", "ver":
		return "read"
	case "codebase", "codebases", "repository", "repositories", "repo", "repositorio", "repositorios":
		return "codebase"
	}
	return strings.TrimSuffix(strings.TrimSuffix(term, "es"), "s")
}

func termPrefixMatch(term string, set map[string]bool) bool {
	if len(term) < 4 {
		return false
	}
	for candidate := range set {
		if len(candidate) >= 4 && (strings.HasPrefix(candidate, term) || strings.HasPrefix(term, candidate)) {
			return true
		}
	}
	return false
}

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "for": true, "from": true, "i": true, "in": true,
	"is": true, "it": true, "my": true, "of": true, "on": true, "or": true,
	"that": true, "the": true, "this": true, "to": true, "want": true, "with": true,
	"de": true, "del": true, "el": true, "en": true, "esta": true, "este": true,
	"la": true, "las": true, "lo": true, "los": true, "mi": true, "necesito": true,
	"o": true, "para": true, "por": true, "que": true, "quiero": true, "un": true,
	"una": true, "unos": true, "unas": true, "y": true,
}

// capabilitySummary gives lazy-mode agents a cheap map of capability families
// without advertising every downstream tool schema.
func capabilitySummary(cfg *config.Config, scope *agentScope) string {
	if cfg == nil {
		return ""
	}
	var items []string
	for _, name := range cfg.EnabledServers() {
		if !scope.allowsServer(name) {
			continue
		}
		srv := cfg.Servers[name]
		hint := ""
		if tools, restricted := scope.allowedToolNames(name); restricted {
			if len(tools) == 0 {
				continue
			}
			hint = allowedToolsHint(tools)
		} else {
			hint = srv.Description
			if len(srv.UseWhen) > 0 {
				hint = strings.Join(srv.UseWhen, "; ")
			}
			if hint == "" && len(srv.Tags) > 0 {
				hint = strings.Join(srv.Tags, ", ")
			}
		}
		hint = clipUTF8(strings.Join(strings.Fields(hint), " "), maxCapabilityHintBytes)
		if hint == "" {
			items = append(items, name)
		} else {
			items = append(items, name+": "+hint)
		}
	}
	var summary strings.Builder
	for i, item := range items {
		separator := ""
		if summary.Len() > 0 {
			separator = " | "
		}
		remaining := len(items) - i
		suffix := ""
		if remaining > 1 {
			suffix = fmt.Sprintf(" | +%d more; call mcphub_list_servers", remaining-1)
		}
		if summary.Len()+len(separator)+len(item)+len(suffix) > maxCapabilitySummaryBytes {
			if summary.Len() == 0 {
				return clipUTF8(item, maxCapabilitySummaryBytes)
			}
			remainingSuffix := fmt.Sprintf(" | +%d more; call mcphub_list_servers", remaining)
			if summary.Len()+len(remainingSuffix) <= maxCapabilitySummaryBytes {
				summary.WriteString(remainingSuffix)
			}
			break
		}
		summary.WriteString(separator)
		summary.WriteString(item)
	}
	return summary.String()
}

func allowedToolsHint(tools []string) string {
	const prefix = "allowed tools: "
	var hint strings.Builder
	hint.WriteString(prefix)
	for i, tool := range tools {
		separator := ""
		if i > 0 {
			separator = ", "
		}
		remaining := len(tools) - i - 1
		suffix := ""
		if remaining > 0 {
			suffix = fmt.Sprintf(" (+%d more)", remaining)
		}
		if hint.Len()+len(separator)+len(tool)+len(suffix) > maxCapabilityHintBytes {
			if i == 0 {
				return clipUTF8(prefix+tool, maxCapabilityHintBytes)
			}
			omittedSuffix := fmt.Sprintf(" (+%d more)", len(tools)-i)
			if hint.Len()+len(omittedSuffix) <= maxCapabilityHintBytes {
				hint.WriteString(omittedSuffix)
			}
			break
		}
		hint.WriteString(separator)
		hint.WriteString(tool)
	}
	return hint.String()
}

const (
	maxCapabilitySummaryBytes = 2048
	maxCapabilityHintBytes    = 256
)

func clipUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes - len("…")
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	if end <= 0 {
		return ""
	}
	return value[:end] + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
