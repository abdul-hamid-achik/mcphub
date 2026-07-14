<script setup lang="ts">
const routes = [
  {
    intent: 'Fetch a public HTTP response as Markdown for source-backed research',
    signals: ['webpage', 'HTTP', 'Markdown', 'research'],
    server: 'hitspec',
    tool: 'hitspec__hitspec_fetch',
    outcome: 'Bounded HTTP response → inline Markdown',
    tone: 'teal',
  },
  {
    intent: 'Investigate evidence across code, CLI output, and databases',
    signals: ['evidence', 'case file', 'tool routing', 'verification'],
    server: 'cortex',
    tool: 'cortex__cortex_investigate',
    outcome: 'Bounded investigation with retained evidence',
    tone: 'sky',
  },
  {
    intent: 'Inspect the repository plan before developing a feature',
    signals: ['repository', 'plan', 'convergence', 'read-only'],
    server: 'bob',
    tool: 'bob__bob_plan',
    outcome: 'Deterministic plan and digest before edits',
    tone: 'rose',
  },
]
</script>

<template>
  <section class="mh-section routes-wrap" aria-labelledby="routes-title">
    <div class="routes-copy">
      <div>
        <p class="mh-kicker">Capability routing</p>
        <h2 id="routes-title" class="mh-h2">Unpinned does not mean unavailable</h2>
      </div>
      <p class="mh-sub">
        In lazy mode, mcphub keeps the catalog out of the prompt but not out of reach.
        Tool metadata and your <code>use_when</code> hints stay searchable, so an agent can
        resolve the current task, inspect the match, and call it through the gateway.
      </p>
    </div>

    <div class="mh-chassis routes-console">
      <div class="routes-bar">
        <span>example catalog routes</span>
        <span class="routes-state">resolve → describe → call</span>
      </div>

      <ol class="routes-list">
        <li v-for="route in routes" :key="route.server" class="route-row">
          <div class="route-intent">
            <span class="route-label">current task</span>
            <p>“{{ route.intent }}”</p>
          </div>

          <div class="route-match">
            <span class="route-label">matched signals</span>
            <div class="signal-list">
              <code v-for="signal in route.signals" :key="signal">{{ signal }}</code>
            </div>
          </div>

          <div :class="['route-target', route.tone]">
            <span class="route-label">recommended capability</span>
            <strong>{{ route.server }}</strong>
            <code>{{ route.tool }}</code>
            <small>{{ route.outcome }}</small>
          </div>
        </li>
      </ol>

      <div class="routes-footer">
        <div class="route-legend">
          <span><i class="legend-dot pinned" aria-hidden="true" />pinned: advertised directly</span>
          <span><i class="legend-dot lazy" aria-hidden="true" />unpinned: resolved on demand</span>
          <span><i class="legend-dot off" aria-hidden="true" />disabled: unavailable</span>
        </div>
        <a href="/guide/contextual-routing">See the discovery contract <span aria-hidden="true">→</span></a>
      </div>
    </div>
  </section>
</template>

<style scoped>
.routes-wrap {
  padding-top: 76px;
}

.routes-copy {
  display: grid;
  grid-template-columns: minmax(0, 0.9fr) minmax(320px, 1.1fr);
  gap: 56px;
  align-items: end;
  margin-bottom: 28px;
}

.routes-copy .mh-sub {
  margin: 0;
}

.routes-copy code {
  color: var(--vp-c-brand-1);
}

.routes-console {
  color: #dbe4ec;
}

.routes-bar,
.routes-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding: 12px 18px;
  font-family: var(--vp-font-family-mono);
  font-size: 11px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: #7d8b99;
}

.routes-bar {
  border-bottom: 1px solid var(--mh-panel-edge);
}

.routes-state {
  color: var(--mh-amber);
}

.routes-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.route-row {
  display: grid;
  grid-template-columns: minmax(240px, 1.2fr) minmax(220px, 0.9fr) minmax(260px, 1fr);
  min-height: 142px;
}

.route-row + .route-row {
  border-top: 1px solid var(--mh-panel-edge);
}

.route-intent,
.route-match,
.route-target {
  min-width: 0;
  padding: 22px;
}

.route-match,
.route-target {
  border-left: 1px solid var(--mh-panel-edge);
}

.route-label {
  display: block;
  margin-bottom: 10px;
  font-family: var(--vp-font-family-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: #66737f;
}

.route-intent p {
  margin: 0;
  max-width: 34ch;
  font-family: var(--mh-display);
  font-size: 17px;
  font-weight: 600;
  line-height: 1.45;
  color: #f0f4f8;
}

.signal-list {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}

.signal-list code {
  padding: 4px 8px;
  border: 1px solid #293540;
  border-radius: 6px;
  background: #131b24;
  color: #9aa8b5;
  font-size: 11px;
}

.route-target {
  position: relative;
  background: rgba(255, 255, 255, 0.018);
}

.route-target::before {
  position: absolute;
  top: 0;
  bottom: 0;
  left: -1px;
  width: 2px;
  content: '';
  background: var(--route-color);
}

.route-target.teal { --route-color: var(--mh-teal); }
.route-target.sky { --route-color: var(--mh-sky); }
.route-target.rose { --route-color: var(--mh-rose); }

.route-target strong,
.route-target code,
.route-target small {
  display: block;
}

.route-target strong {
  margin-bottom: 5px;
  color: var(--route-color);
  font-family: var(--mh-display);
  font-size: 17px;
}

.route-target code {
  overflow: hidden;
  padding: 0;
  background: transparent;
  color: #c3cdd6;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.route-target small {
  margin-top: 9px;
  color: #7d8b99;
  font-size: 12px;
  line-height: 1.4;
}

.routes-footer {
  border-top: 1px solid var(--mh-panel-edge);
  letter-spacing: 0;
  text-transform: none;
}

.route-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
}

.route-legend span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.legend-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #66737f;
}

.legend-dot.pinned { background: var(--mh-amber); }
.legend-dot.lazy { background: var(--mh-teal); }
.legend-dot.off { background: #4b5560; }

.routes-footer a {
  flex: none;
  color: var(--mh-amber);
  font-weight: 600;
  text-decoration: none;
}

.routes-footer a:hover {
  color: #ffb945;
}

.routes-footer a:focus-visible {
  outline: 2px solid var(--mh-amber);
  outline-offset: 3px;
}

@media (max-width: 900px) {
  .routes-copy {
    grid-template-columns: 1fr;
    gap: 8px;
  }

  .route-row {
    grid-template-columns: 1fr 1fr;
  }

  .route-intent {
    grid-column: 1 / -1;
    border-bottom: 1px solid var(--mh-panel-edge);
  }

  .route-match {
    border-left: 0;
  }
}

@media (max-width: 640px) {
  .routes-wrap {
    padding-top: 60px;
  }

  .routes-bar,
  .routes-footer {
    align-items: flex-start;
    flex-direction: column;
  }

  .route-row {
    grid-template-columns: 1fr;
  }

  .route-intent,
  .route-match,
  .route-target {
    grid-column: auto;
    padding: 18px;
    border-left: 0;
  }

  .route-match {
    border-top: 1px solid var(--mh-panel-edge);
  }

  .route-target {
    border-top: 1px solid var(--mh-panel-edge);
  }

  .route-target::before {
    top: -1px;
    right: 0;
    bottom: auto;
    left: 0;
    width: auto;
    height: 2px;
  }
}
</style>
