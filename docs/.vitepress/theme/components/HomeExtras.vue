<script setup lang="ts">
import { ref } from 'vue'

// Real CLI output captured from a live setup (paths shortened).
const tabs = ['sync', 'list', 'stats'] as const
const active = ref<(typeof tabs)[number]>('sync')

const steps = [
  {
    cmd: 'mcphub init',
    text: 'Write mcphub.yaml once — or import the servers your agents already declare with --from-agents.',
  },
  {
    cmd: 'mcphub sync --write',
    text: 'Push the right config into every harness. Dry-run by default, a timestamped .bak before any write.',
  },
  {
    cmd: 'mcphub mcp serve',
    text: 'Your agent connects to one gateway that proxies everything — and every call is recorded locally.',
  },
]

const harnesses = [
  'Claude Code', 'opencode', 'Codex', 'Copilot CLI', 'Qwen Code', 'Gemini CLI',
  'Kilo Code', 'Kimi Code CLI', 'Crush', 'Forge', 'Hermes', 'local-agent',
]
</script>

<template>
  <section class="mh-section extras">
    <!-- terminal -->
    <p class="mh-kicker">See it run</p>
    <h2 class="mh-h2">Real output, not a mockup</h2>
    <p class="mh-sub">
      One YAML file in, twelve synced harnesses out — and a local ledger of what every
      server actually costs you.
    </p>

    <div class="mh-chassis term">
      <div class="term-bar">
        <span class="dot r" /><span class="dot y" /><span class="dot g" />
        <div class="term-tabs" role="tablist" aria-label="mcphub commands">
          <button v-for="t in tabs" :key="t" role="tab" :aria-selected="active === t"
            :class="['term-tab', { on: active === t }]" @click="active = t">
            mcphub {{ t }}
          </button>
        </div>
      </div>

      <pre v-if="active === 'sync'" class="term-body"><code><span class="p">$</span> mcphub sync
<span class="a">»</span> <span class="s">claude</span>  (claude, gateway) → <span class="d">~/.claude.json</span>
    <span class="u">update</span>   mcphub
<span class="a">»</span> <span class="s">codex</span>  (codex, gateway) → <span class="d">~/.codex/config.toml</span>
    <span class="u">update</span>   mcphub
<span class="a">»</span> <span class="s">copilot</span>  (copilot, gateway) → <span class="d">~/.copilot/mcp-config.json</span>
    <span class="u">update</span>   mcphub
<span class="a">»</span> <span class="s">crush</span>  (crush, gateway) → <span class="d">~/.config/crush/crush.json</span>
    <span class="u">update</span>   mcphub
<span class="a">»</span> <span class="s">hermes</span>  (hermes, gateway) → <span class="d">~/.hermes/config.yaml</span>
    <span class="u">update</span>   mcphub
<span class="a">»</span> <span class="s">opencode</span>  (opencode, gateway) → <span class="d">~/.config/opencode/opencode.json</span>
    <span class="u">update</span>   mcphub

<span class="d">Dry run. Re-run with --write to apply (a .bak is saved first).</span></code></pre>

      <pre v-else-if="active === 'list'" class="term-body"><code><span class="p">$</span> mcphub list
<span class="d">SERVER      STATE  KIND    TARGET                    TAGS</span>
<span class="s">bob</span>         <span class="ok">on</span>     stdio   /opt/homebrew/bin/bob     builder,code
<span class="s">codemap</span>     <span class="ok">on</span>     stdio   codemap                   code,search
<span class="s">cortex</span>      <span class="ok">on</span>     stdio   ~/go/bin/cortex           kernel,orchestration
<span class="s">glyph</span>       <span class="ok">on</span>     stdio   glyph                     tui,testing
<span class="s">monitor</span>     <span class="ok">on</span>     stdio   ~/bin/monitor             ops
<span class="s">obsidian</span>    <span class="ok">on</span>     remote  https://127.0.0.1:27124   notes,knowledge
<span class="s">vecgrep</span>     <span class="ok">on</span>     stdio   vecgrep                   code,search</code></pre>

      <pre v-else class="term-body"><code><span class="p">$</span> mcphub stats
Totals (all time): <span class="ok">474 calls</span>, 32 errors, ~622k tokens

<span class="d">SERVER      CALLS  ERRORS  AVG_MS  EST_TOKENS</span>
<span class="s">cortex</span>      169    2       1170    156367
<span class="s">cairntrace</span>  130    14      8317    151104
<span class="s">obsidian</span>    81     4       29      120711
<span class="s">codemap</span>     33     1       6343    28844
<span class="s">vecgrep</span>     21     5       139047  10031
<span class="s">glyph</span>       15     1       224     5814</code></pre>
    </div>

    <!-- how it works -->
    <div class="steps-head">
      <p class="mh-kicker">How it works</p>
      <h2 class="mh-h2">Three commands, and you never hand-edit a config again</h2>
    </div>
    <ol class="steps">
      <li v-for="(s, i) in steps" :key="s.cmd" class="step">
        <span class="step-n mono">{{ i + 1 }}</span>
        <code class="step-cmd">{{ s.cmd }}</code>
        <p class="step-text">{{ s.text }}</p>
      </li>
    </ol>

    <!-- harness strip -->
    <div class="strip">
      <p class="strip-label mh-kicker">Syncs into 12 harnesses</p>
      <ul class="chips">
        <li v-for="h in harnesses" :key="h" class="chip mono">{{ h }}</li>
      </ul>
    </div>

    <!-- cta -->
    <div class="cta mh-chassis">
      <h2 class="cta-title">Stop hand-editing agent configs.</h2>
      <p class="cta-sub mono">n servers → 1 hub → every agent</p>
      <div class="cta-actions">
        <a class="cta-btn brand" href="/guide/getting-started">Get started</a>
        <a class="cta-btn alt" href="https://github.com/abdul-hamid-achik/mcphub" rel="noopener">GitHub</a>
      </div>
    </div>
  </section>
</template>

<style scoped>
.extras { padding-top: 72px; padding-bottom: 96px; }
.mono { font-family: var(--vp-font-family-mono); }

/* terminal */
.term { color: #dbe4ec; }
.term-bar {
  display: flex; align-items: center; gap: 8px;
  padding: 10px 16px;
  border-bottom: 1px solid var(--mh-panel-edge);
}
.dot { width: 11px; height: 11px; border-radius: 50%; }
.dot.r { background: #ff5f57; } .dot.y { background: #febc2e; } .dot.g { background: #28c840; }
.term-tabs { display: flex; gap: 4px; margin-left: 14px; overflow-x: auto; }
.term-tab {
  font-family: var(--vp-font-family-mono);
  font-size: 12px;
  padding: 5px 12px;
  border-radius: 7px;
  color: #7d8b99;
  white-space: nowrap;
  transition: color 0.2s, background 0.2s;
}
.term-tab:hover { color: #dbe4ec; }
.term-tab.on { color: var(--mh-amber); background: rgba(245, 165, 36, 0.1); }

.term-body {
  margin: 0;
  padding: 20px 22px;
  font-family: var(--vp-font-family-mono);
  font-size: 13px;
  line-height: 1.7;
  overflow-x: auto;
  min-height: 330px;
}
.term-body .p { color: var(--mh-amber); font-weight: 700; }
.term-body .a { color: var(--mh-amber); }
.term-body .s { color: var(--mh-teal); }
.term-body .u { color: var(--mh-violet); }
.term-body .d { color: #66737f; }
.term-body .ok { color: var(--mh-lime); }

/* steps */
.steps-head { margin-top: 84px; }
.steps {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 16px;
  margin: 28px 0 0;
  padding: 0;
  list-style: none;
}
.step {
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  padding: 22px;
  background: var(--vp-c-bg-soft);
}
.step-n {
  display: inline-flex; align-items: center; justify-content: center;
  width: 26px; height: 26px;
  border-radius: 7px;
  font-size: 13px; font-weight: 700;
  color: var(--vp-button-brand-text);
  background: var(--mh-amber);
  margin-bottom: 14px;
}
.step-cmd {
  display: block;
  font-family: var(--vp-font-family-mono);
  font-size: 14px;
  font-weight: 600;
  color: var(--vp-c-brand-1);
  background: transparent;
  padding: 0;
  margin-bottom: 8px;
}
.step-text { color: var(--vp-c-text-2); font-size: 14px; line-height: 1.6; margin: 0; }

/* harness strip */
.strip { margin-top: 84px; text-align: center; }
.chips {
  display: flex; flex-wrap: wrap; justify-content: center;
  gap: 8px;
  margin: 18px 0 0; padding: 0; list-style: none;
}
.chip {
  font-size: 12.5px;
  padding: 6px 14px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 999px;
  color: var(--vp-c-text-2);
  transition: border-color 0.2s, color 0.2s;
}
.chip:hover { border-color: var(--mh-amber); color: var(--vp-c-text-1); }

/* cta */
.cta {
  margin-top: 96px;
  padding: 56px 32px;
  text-align: center;
  background:
    radial-gradient(600px 200px at 50% 0%, rgba(245, 165, 36, 0.12), transparent),
    var(--mh-ink);
}
.cta-title {
  font-family: var(--mh-display);
  font-size: 30px;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: #f0f4f8;
  margin: 0 0 10px;
  border: none; padding: 0;
}
.cta-sub { color: #7d8b99; font-size: 14px; margin: 0 0 28px; }
.cta-actions { display: flex; justify-content: center; gap: 12px; flex-wrap: wrap; }
.cta-btn {
  display: inline-block;
  padding: 10px 24px;
  border-radius: 10px;
  font-weight: 600;
  font-size: 14px;
  text-decoration: none;
  transition: background 0.2s, border-color 0.2s;
}
.cta-btn.brand { background: var(--mh-amber); color: #1a1104; }
.cta-btn.brand:hover { background: #ffb945; }
.cta-btn.alt { border: 1px solid var(--mh-panel-edge); color: #dbe4ec; }
.cta-btn.alt:hover { border-color: var(--mh-amber); }
</style>
