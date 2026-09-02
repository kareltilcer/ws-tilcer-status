# Design Handoff — Status v3 (Feedback)

> For: **Claude Design** · Owner: Karel · Written 2026-09-02
> Source of truth: `PRD.md` **§V3-1…§V3-11** (behaviour + data) and `openapi.yaml` **0.3.0** (exact response shapes). This brief tells you **what to design and why**; those tell you **what data exists**. `HANDOFF-design.md` covers v2 and its screens are unchanged — read it for the existing visual language before starting.
>
> ⚠ **v2 owed two deliverables that were never produced** — the `design:accessibility-review` report and the `design:design-handoff` redline (v2 §12, items 5 and 6). v3 asks for both again, and this time the accessibility one is not optional: see §9.

## 1. What is new, and why it is different

v2 was one screen for one person. **v3 has two audiences, and the second has never used this product and never will.**

- **Karel, in the dashboard** — the existing audience. Gains an inbox, a report detail view, a badge on the board and a panel on site detail. This is more of what v2 already is, in the language v2 already speaks: **English, dark by default, inline oklch tokens, no Tailwind**.
- **A household member, in someone else's app** — new. Kája hits a bug in `home.tilcer.cz`, clicks a button, describes what happened, attaches a photo of her screen, and never learns that a service called "status" exists. She sees **Czech**, on a **light page**, probably on a **phone**.

⚠ **These two are not one design system with two skins.** The widget shares no tokens, no stylesheet and no framework with the dashboard — it is a separate bundle rendering into a closed shadow root inside a page it does not control. Treat them as two briefs that happen to arrive together.

---

# Part A — The widget

## 2. Constraints that are already decided

Do not re-litigate these; they came out of the scoping interview and are recorded as V3-D decisions.

- **V3-D55 — neutral and self-contained.** Its own quiet visual language: a light surface, one neutral accent, one radius, a system font stack. **Not status-branded** — a dark oklch panel dropped onto `home`'s light Tailwind pages reads as something broken rather than as something belonging to another service. **Not host-themeable** — a `--sfb-*` variable contract is design surface to specify, document and support for three apps styled by one person. It should read as *a reporting tool that arrived with the page*.
- **V3-D56 — a floating launcher *and* a programmatic trigger.** Default is a floating button (`data-position`); `StatusFeedback.open()` lets a host open the dialog from its own menu item, with `data-launcher="none"` suppressing the floating one. **Design both entry points.**
- **Czech and English**, chosen per site. Czech is the default and the common case.
- **File picker only** — no in-page screenshot, no screen recording. The user attaches a file they already made.
- ≤3 files, ≤10 MB per image, ≤50 MB per video. `image/png|jpeg|webp|gif`, `video/mp4|webm`.
- **The dialog asks what kind of thing this is**: *Chyba / Nápad / Něco jiného*, defaulting to Chyba.
- **One-way.** The reporter gets a thank-you and a reference code. There is no reply, no inbox, no notification, and nothing to design for a conversation.

## 3. Design goals for the widget

- **Openable and finished in under thirty seconds.** The person is annoyed and wants to get back to what they were doing. Every field beyond the message is optional.
- **It must not look like the host app broke.** A panel that inherits nothing and asserts nothing — quiet, obviously deliberate.
- **Honest about what it sends.** See §5.3: this is the one place the widget must be slightly *more* talkative than is comfortable.
- **Phone-first, in practice.** The desktop case is easier; design the 375 px case first and let it grow.
- **Never in the way.** A floating launcher competes with whatever the host already put in that corner. Design it to be small, low-contrast at rest, and out of the way of typical bottom-bar navigation.

## 4. The widget — anatomy and states

Design every one of these. The flow is short but it has more failure states than it looks.

### 4.1 Launcher
Resting, hover, focus-visible, and the `data-position` variants (four corners). Consider how it behaves over a host page that scrolls, and on a phone where a bottom-right button may sit on top of the host's own controls.

### 4.2 Dialog — the form
- **Kind** — three options, one tap. A segmented control is the obvious form; propose otherwise if you disagree.
- **Message** — the only required field. Multi-line, ≤4 000 chars.
- **Attachments** — up to three. Design the empty affordance, a file chip with name/size/remove, and the state where three are attached and the control is disabled.
- **What will be sent** — §5.3.
- **Submit.**

### 4.3 Uploading
Per-file progress, **sequential** (one file at a time — a 50 MB video and two screenshots in parallel on household wifi is worse than in series). Design: a file waiting, a file uploading, a file done, a file failed. The dialog must not look frozen while a 50 MB video moves.

### 4.4 Success
The thank-you plus the **reference code** (`R-7QK2`) — a short code the person can quote to Karel in person or in chat. It is the only thing they take away, so make it copyable and unmissable, and say what it is for.

### 4.5 The failure states — design all of them
| State | What happened | What the person needs |
|---|---|---|
| File too large | Over the image or video cap | Which file, what the limit is, that they can still send the report without it |
| Wrong file type | Outside the allow-list | The list, in plain words ("obrázek nebo video") |
| Too many attempts | `429` | That it is temporary, not that they did something wrong |
| Send failed | Network, `5xx` | A retry that does not lose what they typed |
| Upload failed, report sent | The text landed, a file did not | ⚠ **Reassurance, not alarm** — the report exists and Karel can see it. This is the subtle one |
| Feedback turned off | The kill switch | ⚠ **Nothing at all.** The launcher never renders. There is no screen to design here — this row exists so you do not design one |

### 4.6 Not a state
There is no "your report was fixed", no status lookup, no history of what this person sent before. If a design implies any of those, it is wrong.

## 5. Three things that need real design thought

### 5.1 The launcher's resting weight
It sits on every page of an app someone uses daily. Too loud and it becomes furniture people resent; too quiet and nobody finds it when they need it. This is the single most-seen element in v3 and it will be seen ten thousand times more often than it is clicked.

### 5.2 The reference code
It has no function in the product — nothing looks it up, no route accepts it from a reporter. Its entire job is to let a person say "I sent you R-7QK2" and be understood. Design it to be **read aloud and written down**: the alphabet already excludes I, L, O and U for that reason. Whether it deserves a copy button or just large type is your call.

### 5.3 ⚠ The "what will be sent" disclosure — the ethical centre of this design
The widget always sends the page URL, the referrer, viewport, user agent and locale. On sites where Karel has enabled it, it also sends **the last 50 lines of console output and the last JavaScript error**.

That console tail can contain anything the host app logged. `home` has a privacy model in which a member's private notes are unreadable by anyone including admins — and a console line could carry a private note's title into status, where Karel reads it. The decision was to keep the capture **off by default** and, where it is on, to **show the reporter what is being attached before they submit** (PRD §V3-8).

**Design that disclosure.** It must be:
- **Visible without a click** for the fact that context is attached;
- **Expandable** to see the actual lines, not a description of them;
- **Not scary.** Most reports come from someone doing Karel a favour. A red warning panel would be wrong; so would burying it.

A person who can see what they are attaching can decline to attach it. That is the whole design requirement, and it is harder than it sounds.

## 6. Czech and English

Both string sets ship inside the bundle; there is no runtime fetch. Fixed vocabulary so far:

| English | Czech |
|---|---|
| Report a problem | **Nahlásit problém** |
| What went wrong? | **Co se pokazilo?** |
| Attach a screenshot or video | **Přiložit snímek nebo video** |
| Send | **Odeslat** |
| Thanks — your report reached us | **Děkujeme — hlášení dorazilo** |
| Reference | **Číslo hlášení** |
| Bug / Idea / Something else | **Chyba / Nápad / Něco jiného** |
| This will also send | **Odešle se také** |
| File is too large | **Soubor je příliš velký** |
| Too many attempts, try again later | **Příliš mnoho pokusů, zkuste to později** |

⚠ **Still to be written**, and worth the `design:ux-copy` skill: every error in §4.5, the disclosure copy in §5.3, the "report sent but a file failed" reassurance, the success screen, and the three `kind` option labels in context.

Plurals are needed for *1 soubor · 2 soubory · 5 souborů* and *1 hlášení · 2 hlášení · 5 hlášení*. MB and kB do not inflect. Czech is the default when `data-lang` is absent.

---

# Part B — The dashboard

Follows v2's existing language exactly: English, dark by default via a single `.light` class, inline styles + oklch custom properties, Hanken Grotesk / IBM Plex Mono. **No Tailwind, no shadcn/ui.**

## 7. Screens

### 7.1 Inbox (`/reports`) — new
One queue across every site, newest first, keyset-paginated. Filter by state, site, kind.

Row content: site, kind, first line of the message, `reporter_label`, relative time, attachment count. States `new → open → resolved | declined`.

Design default / empty ("No reports yet") / loading / error.

⚠ **`new` is the only state that drives the board badge**, so the visual weight of `new` versus the rest matters more than it looks.

### 7.2 Report detail (`/reports/:ref`) — new
- The full message.
- **Attachments** — image inline, `<video>` for clips, and an explicit **"file missing"** treatment for an attachment that never arrived or was swept. This is a real, expected state, not an error.
- **Context block** — page URL, user agent, viewport, locale, release, and the console tail where present. ⚠ **The page URL is not clickable without an explicit action** — it can carry query-string secrets from a badly built host app.
- **Triage** — the four states, plus an **internal note** that only Karel ever sees.
- Delete, with confirmation (it removes the stored files too).

### 7.3 Board — one addition
An **unread count badge** on each site card, from `SiteSummary.open_reports`.

⚠ **The site's colour must not change.** Colour stays the machine signal — reachability and crashes. A person saying "this is confusing" must not make `home` look degraded beside a real outage. The badge has to read as *"someone wrote to you"*, not as *"something is wrong"*, and it sits next to a status pill that means exactly the latter. **That adjacency is the design problem in Part B.**

`open_reports` is **`null`, not `0`**, when the module is not deployed — render no badge at all, not a zero.

### 7.4 Site detail — a feedback panel
The kill switch (`enabled`), `console_capture`, when the widget key was issued, **rotate key** (reusing v2's show-once key modal — it is the same interaction with a different key), and the copyable embed snippet prefilled with this site's id.

⚠ A site whose deployment has no object storage configured gets a **`503`** on enable. Design that: the switch is unavailable and says why, rather than failing on click.

## 8. Component inventory

**Widget (all new):** launcher button · dialog shell · kind selector · file drop/pick control · file chip (waiting / uploading / done / failed) · progress affordance · disclosure block · reference-code display · six error treatments.

**Dashboard:** report row · state chip (4) · kind chip (3) · unread badge · attachment gallery (image / video / missing) · context block · internal-note field · feedback panel on site detail.

## 9. Accessibility — required, and overdue

Target **WCAG 2.1 AA**.

⚠ **Run `design:accessibility-review`, and return the report.** v2 asked for exactly this and it was never delivered — the only design artifact in the repo is `design/v2/status-handoff-v2.zip`. This time the stakes are higher: **the widget is the most non-technical-user-facing surface in the entire fleet**, and it is the only one used by people who did not choose to use it.

Specifically:
- **Focus trap** in the dialog; `Escape` closes and returns focus to the launcher; the whole flow is keyboard-operable.
- **The launcher is a real button** with an accessible name in the right language.
- Touch targets ≥44 px — this is a phone-first surface.
- Contrast for the neutral palette **against unknown backgrounds**. The widget cannot know what is behind it, so the panel needs its own opaque surface rather than relying on the host's.
- The file-failed and report-sent states must be announced, not only shown.
- Dashboard: focus-visible on inbox rows, the state controls, and the confirm dialog.

## 10. Deliverables

1. Hi-fi mockups of the **widget** — launcher, form, uploading, success, and all six failure states, at **375 px and desktop**, in **Czech**, on both a light and a dark host page.
2. Hi-fi mockups of the **dashboard additions** — inbox, report detail, the board badge in context beside the status pill, the site-detail feedback panel — each in default / empty / loading / error.
3. The widget's self-contained token set (surface, accent, radius, type scale), explicitly **not** derived from status's oklch tokens.
4. Component specs for §8.
5. **`design:accessibility-review`** report (§9).
6. **`design:design-handoff`** redline to complement `HANDOFF-engineering-v3.md`.

## 11. Open questions — propose, don't ask

1. **The launcher at rest.** Icon-only or icon+label? Labelled is findable; icon-only is quieter on a page it lives on permanently.
2. **The disclosure.** Inline summary that expands, a separate step before submit, or a persistent side panel in the dialog? §5.3 is the hard one — pick a shape and defend it.
3. **The badge beside the pill.** How do you make "someone wrote to you" clearly *not* a health signal when it sits 8 px from one? Numeral, dot, envelope, count-in-pill — your call.
4. **Modal or sheet on mobile.** A centred dialog or a bottom sheet? The file picker and the keyboard both argue for one of them.
5. **`kind` before or after the message?** Asking first classifies cleanly; asking after lets the person start typing immediately, which matters when they are annoyed.
