<script setup lang="ts">
import { ref } from 'vue'

const cmd = 'brew install abdul-hamid-achik/tap/mcphub'
const copied = ref(false)

async function copy() {
  try {
    await navigator.clipboard.writeText(cmd)
    copied.value = true
    setTimeout(() => (copied.value = false), 1600)
  } catch {
    /* clipboard unavailable — the command is selectable */
  }
}
</script>

<template>
  <div class="install">
    <code class="install-cmd"><span class="prompt">$</span> {{ cmd }}</code>
    <button class="install-copy" type="button" :aria-label="copied ? 'Copied' : 'Copy install command'" @click="copy">
      {{ copied ? 'copied' : 'copy' }}
    </button>
  </div>
</template>

<style scoped>
.install {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin: 18px auto 0;
  padding: 6px 6px 6px 16px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
  background: var(--vp-c-bg-soft);
  max-width: 100%;
}
.install-cmd {
  font-family: var(--vp-font-family-mono);
  font-size: 13.5px;
  color: var(--vp-c-text-1);
  white-space: nowrap;
  overflow-x: auto;
  background: transparent;
}
.prompt { color: var(--vp-c-brand-1); font-weight: 700; margin-right: 6px; }
.install-copy {
  font-family: var(--vp-font-family-mono);
  font-size: 12px;
  font-weight: 600;
  padding: 6px 12px;
  border-radius: 7px;
  color: var(--vp-button-brand-text);
  background: var(--vp-button-brand-bg);
  transition: background 0.2s;
  flex-shrink: 0;
}
.install-copy:hover { background: var(--vp-button-brand-hover-bg); }
</style>
