<script setup lang="ts">
// The product thesis, drawn: many MCP servers, one gateway, one connection.
const servers = [
  { name: 'codemap', color: '#2dd4bf' },
  { name: 'vecgrep', color: '#38bdf8' },
  { name: 'github', color: '#a78bfa' },
  { name: 'bob', color: '#fb7185' },
  { name: 'obsidian', color: '#a3e635' },
  { name: 'postgres', color: '#fdba74' },
]
const agents = ['Claude Code', 'Codex', 'opencode', 'Copilot CLI', 'Gemini CLI', 'Crush']

const nodeY = (i: number) => 48 + i * 62
const portY = (i: number) => 137.5 + i * 25
const cablePath = (i: number) =>
  `M182,${nodeY(i)} C300,${nodeY(i)} 290,${portY(i)} 396,${portY(i)}`
</script>

<template>
  <section class="mh-section patchbay-wrap" aria-label="How mcphub connects servers to agents">
    <div class="mh-chassis patchbay">
      <div class="pb-toolbar">
        <span class="pb-led" aria-hidden="true" />
        <span class="pb-title">patch bay — live topology</span>
        <span class="pb-note">n servers · 1 stdio connection</span>
      </div>
      <div class="pb-scroll">
        <svg viewBox="0 0 960 400" role="img" class="pb-svg"
          aria-label="Six MCP servers wired into the mcphub gateway, which exposes one connection to your agent">
          <!-- cables in -->
          <g v-for="(s, i) in servers" :key="'c' + s.name">
            <path :d="cablePath(i)" pathLength="250" class="cable-base" :stroke="s.color" />
            <path :d="cablePath(i)" pathLength="250" class="cable-pulse" :stroke="s.color"
              :style="{ animationDelay: (i * -0.45) + 's' }" />
          </g>

          <!-- cable out -->
          <path d="M564,200 C660,200 660,200 756,200" pathLength="250" class="cable-base out" stroke="#f5a524" />
          <path d="M564,200 C660,200 660,200 756,200" pathLength="250" class="cable-pulse out" stroke="#f5a524" />

          <!-- server nodes -->
          <g v-for="(s, i) in servers" :key="s.name">
            <rect x="24" :y="nodeY(i) - 19" width="158" height="38" rx="8" class="node" />
            <circle cx="44" :cy="nodeY(i)" r="3.5" :fill="s.color" />
            <text x="58" :y="nodeY(i) + 4.5" class="mono label">{{ s.name }}</text>
            <circle cx="182" :cy="nodeY(i)" r="3" :fill="s.color" opacity="0.9" />
          </g>

          <!-- hub -->
          <g>
            <rect x="396" y="110" width="168" height="180" rx="14" class="hub" />
            <circle v-for="(s, i) in servers" :key="'p' + i" cx="396" :cy="portY(i)" r="3.2" :fill="s.color" />
            <text x="480" y="188" text-anchor="middle" class="mono hub-name">mcphub</text>
            <text x="480" y="212" text-anchor="middle" class="mono hub-sub">gateway · stdio</text>
            <text x="480" y="252" text-anchor="middle" class="mono hub-ns">server__tool</text>
            <circle cx="540" cy="132" r="3" class="hub-led" />
            <circle cx="552" cy="132" r="3" class="hub-led" style="animation-delay: -0.9s" />
            <circle cx="564" cy="200" r="3.4" fill="#f5a524" />
          </g>

          <!-- agent chip -->
          <g>
            <rect x="756" y="164" width="180" height="72" rx="10" class="node agent" />
            <text x="776" y="188" class="mono agent-eyebrow">your agent</text>
            <g class="agent-cycle">
              <text v-for="(a, i) in agents" :key="a" x="776" y="218" class="mono agent-name"
                :style="{ animationDelay: (i * 2 - 12) + 's' }">{{ a }}</text>
            </g>
          </g>
        </svg>
      </div>
    </div>
  </section>
</template>

<style scoped>
.patchbay-wrap { margin-top: 40px; }
.patchbay { color: #dbe4ec; }

.pb-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--mh-panel-edge);
  font-family: var(--vp-font-family-mono);
  font-size: 11px;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: #7d8b99;
}
.pb-led {
  width: 7px; height: 7px; border-radius: 50%;
  background: #f5a524;
  box-shadow: 0 0 8px rgba(245, 165, 36, 0.8);
  animation: pbBlink 2.4s infinite;
}
.pb-note { margin-left: auto; }

.pb-scroll { overflow-x: auto; }
.pb-svg { display: block; min-width: 720px; width: 100%; height: auto; padding: 8px 4px; }

.mono { font-family: var(--vp-font-family-mono); }

.node { fill: var(--mh-panel); stroke: var(--mh-panel-edge); stroke-width: 1; }
.node.agent { stroke: rgba(245, 165, 36, 0.45); }
.label { font-size: 13px; fill: #c3cdd6; }

.hub { fill: #131b24; stroke: #f5a524; stroke-width: 1.2; }
.hub-name { font-size: 19px; font-weight: 700; fill: #f5a524; }
.hub-sub { font-size: 11px; fill: #7d8b99; }
.hub-ns { font-size: 12px; fill: #c3cdd6; }
.hub-led { fill: #f5a524; animation: pbBlink 1.8s infinite; }

.cable-base { fill: none; stroke-width: 1.6; opacity: 0.3; }
.cable-base.out { stroke-width: 2.4; opacity: 0.45; }
.cable-pulse {
  fill: none;
  stroke-width: 2.4;
  stroke-linecap: round;
  stroke-dasharray: 14 236;
  stroke-dashoffset: 250;
  animation: pbFlow 2.8s linear infinite;
}
.cable-pulse.out {
  stroke-width: 3.2;
  stroke-dasharray: 20 230;
  animation-duration: 1.6s;
}

.agent-eyebrow { font-size: 11px; fill: #7d8b99; letter-spacing: 0.08em; }
.agent-name { font-size: 15px; font-weight: 600; fill: #f0f4f8; opacity: 0; animation: pbCycle 12s infinite; }

@keyframes pbFlow { to { stroke-dashoffset: 0; } }
@keyframes pbBlink { 0%, 60%, 100% { opacity: 1; } 75% { opacity: 0.25; } }
@keyframes pbCycle {
  0%, 14% { opacity: 1; }
  17%, 100% { opacity: 0; }
}

@media (prefers-reduced-motion: reduce) {
  .cable-pulse { animation: none; stroke-dashoffset: 125; }
  .agent-name { animation: none; }
  .agent-name:first-child { opacity: 1; }
  .pb-led, .hub-led { animation: none; }
}
</style>
