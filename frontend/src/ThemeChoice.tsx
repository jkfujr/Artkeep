import { useEffect, useState } from 'react'
import { Choice } from './ui'

type Mode = 'system' | 'light' | 'dark'

export default function ThemeChoice() {
  const [mode, setMode] = useState<Mode>(() => document.documentElement.dataset.themeMode as Mode || 'system')
  useEffect(() => {
    const media = matchMedia('(prefers-color-scheme: dark)')
    const apply = () => {
      const theme = mode === 'system' ? (media.matches ? 'dark' : 'light') : mode
      document.documentElement.dataset.theme = theme
      document.documentElement.dataset.themeMode = mode
      document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#15181E' : '#F6F7FA')
    }
    apply()
    media.addEventListener('change', apply)
    return () => media.removeEventListener('change', apply)
  }, [mode])
  return <Choice className="theme-choice" label="主题" value={mode} onChange={value => { setMode(value as Mode); try { localStorage.setItem('collectionTheme', value) } catch { /* 浏览器未允许保存偏好。 */ } }} options={[{ key: 'system', label: '跟随设备' }, { key: 'light', label: '浅色' }, { key: 'dark', label: '深色' }]} />
}
