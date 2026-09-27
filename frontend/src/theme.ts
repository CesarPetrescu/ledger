/** The console theme: follow the system (default), or force light or dark. */
export type ThemeChoice = 'system' | 'light' | 'dark'

const KEY = 'ledger-theme'
export const THEME_ORDER: readonly ThemeChoice[] = ['system', 'light', 'dark']

export function readTheme(): ThemeChoice {
  try {
    const value = window.localStorage.getItem(KEY)
    return value === 'light' || value === 'dark' ? value : 'system'
  } catch {
    return 'system'
  }
}

/** Applies a choice to <html data-theme>; storage failures are ignored. */
export function applyTheme(choice: ThemeChoice): void {
  if (choice === 'system') delete document.documentElement.dataset.theme
  else document.documentElement.dataset.theme = choice
  try {
    if (choice === 'system') window.localStorage.removeItem(KEY)
    else window.localStorage.setItem(KEY, choice)
  } catch {
    // Private mode or blocked storage: the choice lasts for this page only.
  }
}
