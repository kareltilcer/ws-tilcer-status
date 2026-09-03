import { describe, expect, it } from 'vitest'
import { czechPlural, resolveLang, STRINGS } from './i18n'

describe('resolveLang', () => {
  it('prefers data-lang, then <html lang>, then Czech', () => {
    expect(resolveLang('en', 'cs')).toBe('en')
    expect(resolveLang(null, 'en-GB')).toBe('en')
    expect(resolveLang(null, null)).toBe('cs')
    // §V3-11: the widget speaks Czech by default and English on data-lang="en".
    expect(resolveLang('de', 'fr')).toBe('cs')
    expect(resolveLang(' CS-cz ', null)).toBe('cs')
  })
})

describe('czechPlural', () => {
  it('picks 1 / 2–4 / 0 and 5+', () => {
    expect(czechPlural(1, 'soubor', 'soubory', 'souborů')).toBe('soubor')
    expect(czechPlural(2, 'soubor', 'soubory', 'souborů')).toBe('soubory')
    expect(czechPlural(4, 'soubor', 'soubory', 'souborů')).toBe('soubory')
    expect(czechPlural(5, 'soubor', 'soubory', 'souborů')).toBe('souborů')
    expect(czechPlural(0, 'soubor', 'soubory', 'souborů')).toBe('souborů')
  })
})

describe('the two string sets', () => {
  it('carry the same keys, so neither language can be missing a screen', () => {
    expect(Object.keys(STRINGS.cs).sort()).toEqual(Object.keys(STRINGS.en).sort())
  })

  it('inflects the attachment hint', () => {
    expect(STRINGS.cs.attachHint(3, '10 MB', '50 MB')).toBe('Až 3 soubory · obrázek do 10 MB, video do 50 MB')
    expect(STRINGS.cs.retryIn(1)).toBe('Zkusit znovu za 1 minutu')
    expect(STRINGS.cs.retryIn(5)).toBe('Zkusit znovu za 5 minut')
    expect(STRINGS.en.retryIn(1)).toBe('Try again in 1 minute')
  })
})
