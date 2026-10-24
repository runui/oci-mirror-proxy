# Contributing translations

Translations of the documentation website are welcome through GitHub pull requests. You can improve a single sentence, translate one document, or help review an existing language. English is the source language and the fallback for untranslated content.

## Where to edit

| Content                                                                   | English source                | Translation                         |
| ------------------------------------------------------------------------- | ----------------------------- | ----------------------------------- |
| Homepage, navigation, accessibility labels, search controls, and SEO text | `locales/en/translation.json` | `locales/{locale}/translation.json` |
| Documentation                                                             | `content/docs/name.mdx`       | `content/docs/name.{locale}.mdx`    |
| Sidebar groups and order                                                  | `content/docs/meta.json`      | `content/docs/meta.{locale}.json`   |

The current language codes are `en`, `zh-CN`, and `ru`. Paths in this guide are relative to `apps/docs/`. Edit the source files, not generated files in `public/`, `.source/`, `.next/`, or `out/`.

## Translate the interface

Use the English JSON as the reference and translate values, not keys. Keep the object structure, array order, and any `{{interpolation}}` placeholders intact. Fumadocs messages use single-brace placeholders such as `{url}` and `{query}`; preserve these too. `displayName` is the language's own name. The `fumadocs` section contains library message identifiers; their parentheses and wording are part of the keys and must not change.

i18next reads these bundled resources with English fallback. Fixed-language translators serve both React components and build-time metadata; there is no translation server or runtime download to configure. Routes, search indexes, and MDX remain managed by Fumadocs. Keep URLs, icons, and executable commands in code rather than introducing them into interface translations.

## Translate a document

1. Copy the English file to `name.{locale}.mdx`.
2. Translate the frontmatter `title` and `description`, prose, table descriptions, image alternatives, and card labels. Start body headings at level two.
3. Preserve command names, flags, configuration keys, versions, example domains, and security warnings. Comments inside command examples may be translated; the commands must retain their meaning. Region labels such as `cn` are catalog identifiers, not language codes to translate.
4. Point documentation links at the translated route: `/ru/docs/installation`, for example. Keep repository links absolute.
5. Check heading links. Fumadocs accepts an explicit identifier such as `## Установка [#installation]`. Use the English heading's identifier so links survive language switching. If an existing translation changes its heading identifier, retain the old identifier with an empty `<span id="old-identifier" />` before the heading. Do not use `{#installation}`, which MDX treats as JavaScript. Export checks verify that each translated page retains its English page's heading anchors.

Documents can be translated incrementally. If a translated file is absent, Fumadocs displays the English original with a localized notice. That fallback uses the English canonical URL, is excluded from the sitemap, and is marked `noindex`. Do not create an untranslated copy just to increase coverage. The translation checker reports missing documents without treating them as failures.

## Validate and format

Use Node.js 22.22 or newer (Node 24 LTS recommended). Install the website dependencies with `npm ci` in this directory. Install the repository's formatter with `npm ci` at the repository root as well.

From `apps/docs/`:

```sh
npm run check:i18n
npm test
npm run types:check
npm run build
npm run check:links
npm run dev
```

`check:i18n` checks interface keys, value types, array structure, and interpolation placeholders, and reports document coverage. The export checks verify language tags, local links and anchors, search, canonical URLs, reciprocal language alternates, structured data, Markdown/LLM exports, and social images. The documentation CI runs these checks for pull requests.

Formatting uses the repository's `.oxfmtrc.json`, including tabs and its MDX settings. From the repository root, format only the files you changed, for example:

```sh
npx --no-install oxfmt --config .oxfmtrc.json --write apps/docs/locales/ru/translation.json apps/docs/content/docs/index.ru.mdx
```

Check the homepage and at least one long document at desktop and mobile widths. Exercise language switching, search, code-copy buttons, and the light/dark/system theme menu. Mention the language and documents in the pull request, and request review from a fluent speaker where possible. Automated checks cannot establish translation quality. When an English source changes, include matching translation updates if you can, or identify the affected translations for follow-up review.

## Add a language

Adding a new language requires a small site integration in addition to translations:

- Register its code in `lib/locales.ts`, import its JSON in `lib/messages.ts`, and add browser-language matching in `lib/preferences.ts`.
- Add the Open Graph locale in `lib/metadata.ts` and the corresponding validation mapping in `scripts/check-seo.mjs`.
- Follow `app/ru/` for static homepage, documentation, layout, and LLM routes. Add LLM response headers in `public/_headers` and a documentation link on the not-found page.
- Add `meta.{locale}.json`, a complete interface dictionary, and any completed MDX translations. Do not add placeholder translations for unfinished documents.
- Add a localized search query to `scripts/check-search.mjs` and language preference tests. Verify the bundled font and generated social images support the script.

The source loader, language selector, sitemap, export checks, and coverage report use the registered languages. Existing English and Chinese URLs must remain unchanged. A pull request proposing a language and its first translations is welcome even if you need help with this integration.
