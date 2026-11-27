# Landing page

Static project page for TDB, authored here and published to the public
repository [`p-v-q/tdb-site`](https://github.com/p-v-q/tdb-site). GitHub Pages
serves it at **<https://p-v-q.github.io/tdb-site/>**.

## Why a second repository

This repository is private and the `p-v-q` organization is on the GitHub Free
plan, where Pages is only available for public repositories. `tdb-site` is
public and holds nothing but the generated page, so the product source stays
private.

## How publishing works

```text
site/            ─┐
docs/img/*.png   ─┼─> make site-publish ─> p-v-q/tdb-site (main) ─> GitHub Pages
docs/demo.gif    ─┘
```

`make site-publish` clones `tdb-site` into `.artifacts/site-publish`, copies the
page next to the screenshots and the demo GIF, and pushes the result. It needs
push access to `tdb-site` (your SSH key) and a Unix-like shell, like the other
Makefile targets.

There is no build step: `index.html`, `styles.css`, `favicon.svg` and
`.nojekyll` are served as-is. Keep every asset reference relative
(`styles.css`, `img/postgres.png`, `demo.gif`); Pages serves the site from a
subpath, so a leading `/` would break.

The generated repository says so in its README: edit the page here, not there.

## Pages setup

In `p-v-q/tdb-site`, **Settings → Pages** is configured once as **Deploy from a
branch**, branch `main`, folder `/ (root)`.

## Local preview

```bash
make site-preview
python -m http.server -d .artifacts/site 8000
```

Then open <http://localhost:8000>. The staging directory mirrors the published
layout, so relative paths behave the same.

## Editing

The copy mirrors the feature tour in [`README.md`](../README.md); update both
when a feature, install command or screenshot changes. The page is dark-only,
uses system fonts, and has no external dependencies.
