# Third-Party Notices

RouteNest includes material from the projects below. Each notice applies to the corresponding material.

## Cline2API

RouteNest is an independent fork of Cline2API.

- Source: https://github.com/luawei1/cline2api
- License: MIT
- Copyright (c) 2026 luawei1

The complete MIT license text and copyright notice are retained in [LICENSE](LICENSE).

## ExtremeRouter provider registry

The provider preset definitions shipped in `internal/providerreg/imported.json` include 90 entries derived from the ExtremeRouter provider registry: preset IDs, display names, base URLs, seed model IDs, notes, and catalog-fetcher hints.

- License: MIT
- Copyright (c) 2024-2026 Raden Salman Alfaridzi (ExtremeRouter, originally 9Router by decolua)

The MIT license text applicable to this material follows:

```text
MIT License

Copyright (c) 2024-2026 Raden Salman Alfaridzi (ExtremeRouter, originally 9Router by decolua)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Admin UI fonts

Inter and IBM Plex Mono are embedded as unmodified Latin WOFF2 fonts in [01_fonts.css](internal/admin/ui/css/01_fonts.css). A Noto Sans SC variable TrueType subset covers every CJK character used by the shipped admin UI. The complete SIL Open Font License 1.1 text, copyright, source URLs and SHA-256 hashes are retained in that stylesheet and shipped with the standalone application. No external font service is contacted at runtime. Existing system fallbacks cover characters outside the embedded subsets.

- Inter: Copyright 2020 The Inter Project Authors; [source and license](https://github.com/google/fonts/tree/main/ofl/inter). Used for the interface face.
- IBM Plex Mono: Copyright 2017 IBM Corp.; [source and license](https://github.com/IBM/plex). Used only for technical identifiers such as model IDs, request IDs, API paths, endpoints, keys and JSON.
- Noto Sans SC: Copyright 2014-2021 Adobe and Google LLC; [source and license](https://github.com/notofonts/noto-cjk). Subset from the [Google Fonts distribution](https://github.com/google/fonts/tree/main/ofl/notosanssc) using fonttools and used only as the interface's CJK fallback.
