import DefaultTheme from 'vitepress/theme'
import { h } from 'vue'
import PatchBay from './components/PatchBay.vue'
import InstallCmd from './components/InstallCmd.vue'
import HomeExtras from './components/HomeExtras.vue'
import './custom.css'

export default {
  extends: DefaultTheme,
  Layout() {
    return h(DefaultTheme.Layout, null, {
      'home-hero-info-after': () => h(InstallCmd),
      'home-hero-after': () => h(PatchBay),
      'home-features-after': () => h(HomeExtras),
    })
  },
}
