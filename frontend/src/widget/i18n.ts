// Both string sets ship inside the bundle — there is no runtime translation
// fetch (FR-24). `strings_version` in the config response exists only so a
// mismatch between this file and the server's idea of it is diagnosable.
//
// The vocabulary is the design copy deck (HANDOFF-design-v3.md §6 and the copy
// tab of `design/v3/Feedback widget.dc.html`). Its four tone rules, applied
// throughout: say what happened and then what the person can do; a file problem
// is never phrased as a failed report; no apologies, which imply a relationship
// the widget does not have; and no jargon — "obrázek nebo video" is the
// allow-list, not a list of MIME types.

import type { Lang } from './types'

/** czechPlural picks the 1 / 2–4 / 0,5+ form. Czech needs three; English needs
 *  two, and `en` below simply ignores the third. */
export function czechPlural(n: number, one: string, few: string, many: string): string {
  if (n === 1) return one
  if (n >= 2 && n <= 4) return few
  return many
}

export interface Strings {
  lang: Lang
  title: string
  close: string
  remove: string

  msgLabel: string
  msgPlaceholder: string
  msgHint: string
  msgRequired: string

  kindLabel: string
  kinds: [string, string, string]

  attach: string
  attachFullBtn: string
  attachHint: (maxFiles: number, image: string, video: string) => string
  attachHintFull: (maxFiles: number) => string

  discLead: string
  discSummary: string
  discSummaryConsole: string
  discShow: string
  discHide: string
  discKeys: {
    pageUrl: string
    referrer: string
    browser: string
    viewport: string
    locale: string
    release: string
  }
  consoleLead: string
  consoleNote: string
  consoleOptOut: string

  send: string
  sending: string

  yourReport: string
  uploadingN: (index: number, total: number) => string
  uploadNote: string
  fileWaiting: string
  fileDone: string
  fileFailed: string

  successTitle: string
  successBody: string
  refLabel: string
  copy: string
  copied: string
  refHelp: string

  tooLargeTitle: string
  tooLargeBody: (video: boolean, limit: string, actual: string) => string
  cannotAttach: string
  wrongTypeTitle: string
  wrongTypeBody: string
  tooManyTitle: string
  tooManyBody: (maxFiles: number) => string

  rateTitle: string
  rateBody: string
  retryIn: (minutes: number) => string

  sendFailTitle: string
  sendFailBody: string
  sendAgain: string

  uploadFailedTitle: string
  uploadFailedBody: (names: string[]) => string

  discardTitle: string
  discard: string
  keepEditing: string
}

const cs: Strings = {
  lang: 'cs',
  title: 'Nahlásit problém',
  close: 'Zavřít',
  remove: 'Odebrat soubor',

  msgLabel: 'Co se pokazilo?',
  msgPlaceholder: 'Napište, co jste dělali a co se stalo. Klidně vlastními slovy.',
  msgHint: 'Stačí pár vět. Ostatní je nepovinné.',
  msgRequired: 'Napište prosím pár slov o tom, co se stalo.',

  kindLabel: 'O co jde?',
  kinds: ['Chyba', 'Nápad', 'Něco jiného'],

  attach: 'Přiložit snímek nebo video',
  attachFullBtn: 'Víc souborů už nejde',
  attachHint: (n, image, video) =>
    `Až ${n} ${czechPlural(n, 'soubor', 'soubory', 'souborů')} · obrázek do ${image}, video do ${video}`,
  attachHintFull: (n) =>
    `Máte přiložené ${n} ${czechPlural(n, 'soubor', 'soubory', 'souborů')} — to je maximum.`,

  discLead: 'Odešle se také',
  discSummary: 'adresa stránky, prohlížeč a velikost okna',
  discSummaryConsole: 'adresa stránky, prohlížeč, velikost okna a výpis z konzole',
  discShow: 'Zobrazit, co se odešle',
  discHide: 'Skrýt',
  discKeys: {
    pageUrl: 'adresa stránky',
    referrer: 'odkud jste přišli',
    browser: 'prohlížeč',
    viewport: 'velikost okna',
    locale: 'jazyk',
    release: 'verze aplikace',
  },
  consoleLead: 'Posledních 50 řádků z konzole prohlížeče',
  consoleNote: 'Pomáhá najít příčinu. Když v něm uvidíte něco osobního, nechte ho doma.',
  consoleOptOut: 'Výpis z konzole neposílat',

  send: 'Odeslat',
  sending: 'Odesílám…',

  yourReport: 'Vaše hlášení',
  uploadingN: (i, total) => `Nahrávám soubor ${i} ze ${total}`,
  uploadNote: 'Soubory se nahrávají po jednom. Text hlášení už je odeslaný — zavřít můžete kdykoli.',
  fileWaiting: 'čeká',
  fileDone: 'nahráno',
  fileFailed: 'nepovedlo se',

  successTitle: 'Děkujeme — hlášení dorazilo',
  successBody: 'Karel ho uvidí ve svém přehledu. Odpověď sem nepřijde.',
  refLabel: 'Číslo hlášení',
  copy: 'Zkopírovat',
  copied: 'Zkopírováno',
  refHelp: 'Uschovejte si ho. Když se budete chtít na hlášení zeptat, stačí říct tohle číslo.',

  tooLargeTitle: 'Soubor je příliš velký',
  tooLargeBody: (video, limit, actual) =>
    `${video ? 'Video' : 'Obrázek'} může mít nejvýš ${limit}, tenhle má ${actual}. Zkuste menší soubor — hlášení jde odeslat i bez něj.`,
  cannotAttach: 'nelze přiložit',
  wrongTypeTitle: 'Tenhle soubor přiložit neumíme',
  wrongTypeBody:
    'Přiložte obrázek (PNG, JPG, WebP, GIF) nebo video (MP4, WebM). Dokumenty a archivy neprojdou.',
  tooManyTitle: 'Víc souborů už nejde',
  tooManyBody: (n) =>
    `Přiložit jde nejvýš ${n} ${czechPlural(n, 'soubor', 'soubory', 'souborů')}. Některý napřed odeberte.`,

  rateTitle: 'Příliš mnoho pokusů, zkuste to později',
  rateBody:
    'Za pár minut to půjde znovu. Není to nic, co byste udělali špatně — jen jsme za chvíli dostali hodně hlášení.',
  retryIn: (m) => `Zkusit znovu za ${m} ${czechPlural(m, 'minutu', 'minuty', 'minut')}`,

  sendFailTitle: 'Odeslání se nepovedlo',
  sendFailBody: 'Spojení se přerušilo. Text zůstal vyplněný, nic jste neztratili.',
  sendAgain: 'Odeslat znovu',

  uploadFailedTitle: 'Hlášení dorazilo',
  uploadFailedBody: (names) =>
    `Tohle se nahrát nepovedlo: ${names.join(', ')}. Text hlášení ale dorazil — Karel ho uvidí.`,

  discardTitle: 'Zahodit rozepsané hlášení?',
  discard: 'Zahodit',
  keepEditing: 'Psát dál',
}

const en: Strings = {
  lang: 'en',
  title: 'Report a problem',
  close: 'Close',
  remove: 'Remove file',

  msgLabel: 'What went wrong?',
  msgPlaceholder: 'Tell us what you were doing and what happened. Your own words are fine.',
  msgHint: 'A couple of sentences is plenty. Everything else is optional.',
  msgRequired: 'Please write a few words about what happened.',

  kindLabel: 'What kind of thing is this?',
  kinds: ['Bug', 'Idea', 'Something else'],

  attach: 'Attach a screenshot or video',
  attachFullBtn: 'No more files',
  attachHint: (n, image, video) =>
    `Up to ${n} file${n === 1 ? '' : 's'} · images to ${image}, video to ${video}`,
  attachHintFull: (n) => `${n} file${n === 1 ? '' : 's'} attached — that is the limit.`,

  discLead: 'This will also send',
  discSummary: 'the page address, your browser and window size',
  discSummaryConsole: 'the page address, your browser, window size and console output',
  discShow: 'Show what will be sent',
  discHide: 'Hide',
  discKeys: {
    pageUrl: 'page address',
    referrer: 'came from',
    browser: 'browser',
    viewport: 'window size',
    locale: 'language',
    release: 'app version',
  },
  consoleLead: 'The last 50 lines of browser console output',
  consoleNote: 'It helps find the cause. If you see anything private in it, leave it out.',
  consoleOptOut: 'Do not send the console output',

  send: 'Send',
  sending: 'Sending…',

  yourReport: 'Your report',
  uploadingN: (i, total) => `Uploading file ${i} of ${total}`,
  uploadNote:
    'Files upload one at a time. The report text is already sent — you can close at any time.',
  fileWaiting: 'waiting',
  fileDone: 'uploaded',
  fileFailed: 'failed',

  successTitle: 'Thanks — your report reached us',
  successBody: 'Karel will see it in his dashboard. No reply arrives here.',
  refLabel: 'Reference',
  copy: 'Copy',
  copied: 'Copied',
  refHelp: 'Keep it. If you want to ask about the report, this number is all you need.',

  tooLargeTitle: 'That file is too large',
  tooLargeBody: (video, limit, actual) =>
    `${video ? 'Video' : 'Images'} can be up to ${limit}; this one is ${actual}. Try a smaller file — you can send the report without it.`,
  cannotAttach: 'cannot attach',
  wrongTypeTitle: 'We cannot attach that file',
  wrongTypeBody:
    'Attach an image (PNG, JPG, WebP, GIF) or a video (MP4, WebM). Documents and archives will not go through.',
  tooManyTitle: 'No more files',
  tooManyBody: (n) => `You can attach at most ${n} file${n === 1 ? '' : 's'}. Remove one first.`,

  rateTitle: 'Too many attempts, try again later',
  rateBody:
    'It will work again in a few minutes. Nothing you did was wrong — we just took a lot of reports at once.',
  retryIn: (m) => `Try again in ${m} minute${m === 1 ? '' : 's'}`,

  sendFailTitle: 'Sending did not work',
  sendFailBody: 'The connection dropped. Your text is still here — nothing was lost.',
  sendAgain: 'Send again',

  uploadFailedTitle: 'Your report reached us',
  uploadFailedBody: (names) =>
    `This did not upload: ${names.join(', ')}. The report text did arrive — Karel will see it.`,

  discardTitle: 'Discard this report?',
  discard: 'Discard',
  keepEditing: 'Keep writing',
}

export const STRINGS: Record<Lang, Strings> = { cs, en }

/** resolveLang follows FR-24: `data-lang`, then <html lang>, then Czech — which
 *  is the default because the common case is a household member on a Czech page,
 *  not the one English-reading admin. */
export function resolveLang(attr: string | null, documentLang: string | null): Lang {
  const pick = (v: string | null | undefined): Lang | null => {
    if (!v) return null
    const base = v.trim().toLowerCase().split('-')[0]
    return base === 'en' ? 'en' : base === 'cs' ? 'cs' : null
  }
  return pick(attr) ?? pick(documentLang) ?? 'cs'
}
