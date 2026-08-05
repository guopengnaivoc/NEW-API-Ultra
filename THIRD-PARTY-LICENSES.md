# Third-Party Licenses

This file is a publication-time inventory of direct dependencies used by the
checked-in Go, web, and Electron manifests. Versions are resolved from the
current `go.mod`, `web/bun.lock`, and `electron/package-lock.json` in this
snapshot; they are not a substitute for a complete transitive SBOM.

License labels are engineering triage, not legal clearance. Entries marked
`VERIFY` and the unresolved `go-epay` license evidence require review before
redistributing binaries or operating a hosted service. Preserve each
dependency's upstream notices in addition to this summary.

Docker Compose service images are operational dependencies rather than direct
entries in these application manifests. Their licenses are not covered by the
table below; see `DEPLOYMENT.md` and review each exact image before use.

## Dependency Inventory

| Area | Scope | Ecosystem | Dependency | Resolved version | License / status |
|---|---|---|---|---|---|
| backend | production | Go | `github.com/Azure/go-ntlmssp` | `v0.1.1` | MIT |
| backend | production | Go | `github.com/Calcium-Ion/go-epay` | `v0.0.4` | License status unresolved: repository metadata says MIT, but the v0.0.4 tag has no license file |
| backend | production | Go | `github.com/ClickHouse/clickhouse-go/v2` | `v2.32.0` | Apache-2.0 |
| backend | production | Go | `github.com/abema/go-mp4` | `v1.4.1` | MIT |
| backend | test | Go | `github.com/alicebob/miniredis/v2` | `v2.38.0` | MIT |
| backend | production | Go | `github.com/andybalholm/brotli` | `v1.1.1` | MIT |
| backend | production | Go | `github.com/anknown/ahocorasick` | `v0.0.0-20190904063843-d75dbd5169c0` | MIT |
| backend | production | Go | `github.com/aws/aws-sdk-go-v2` | `v1.41.5` | Apache-2.0 |
| backend | production | Go | `github.com/aws/aws-sdk-go-v2/credentials` | `v1.19.10` | Apache-2.0 |
| backend | production | Go | `github.com/aws/aws-sdk-go-v2/service/bedrockruntime` | `v1.50.4` | Apache-2.0 |
| backend | production | Go | `github.com/aws/smithy-go` | `v1.24.2` | Apache-2.0 |
| backend | production | Go | `github.com/bytedance/gopkg` | `v0.1.3` | Apache-2.0 |
| backend | production | Go | `github.com/casbin/casbin/v2` | `v2.135.0` | Apache-2.0 |
| backend | production | Go | `github.com/expr-lang/expr` | `v1.17.8` | MIT |
| backend | production | Go | `github.com/gin-contrib/cors` | `v1.7.2` | MIT |
| backend | production | Go | `github.com/gin-contrib/gzip` | `v0.0.6` | MIT |
| backend | production | Go | `github.com/gin-contrib/static` | `v0.0.1` | MIT |
| backend | production | Go | `github.com/gin-gonic/gin` | `v1.9.1` | MIT |
| backend | production | Go | `github.com/glebarez/go-sqlite` | `v1.21.2` | BSD-3-Clause |
| backend | production | Go | `github.com/glebarez/sqlite` | `v1.9.0` | MIT |
| backend | production | Go | `github.com/go-audio/aiff` | `v1.1.0` | Apache-2.0 |
| backend | production | Go | `github.com/go-audio/wav` | `v1.1.0` | Apache-2.0 |
| backend | production | Go | `github.com/go-playground/validator/v10` | `v10.20.0` | MIT |
| backend | production | Go | `github.com/go-redis/redis/v8` | `v8.11.5` | BSD-2-Clause |
| backend | production | Go | `github.com/go-sql-driver/mysql` | `v1.7.0` | MPL-2.0 |
| backend | production | Go | `github.com/go-webauthn/webauthn` | `v0.14.0` | BSD-3-Clause |
| backend | production | Go | `github.com/golang-jwt/jwt/v5` | `v5.3.0` | MIT |
| backend | production | Go | `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause |
| backend | production | Go | `github.com/gorilla/websocket` | `v1.5.0` | BSD-2-Clause |
| backend | production | Go | `github.com/grafana/pyroscope-go` | `v1.2.7` | Apache-2.0 |
| backend | production | Go | `github.com/jackc/pgx/v5` | `v5.9.2` | MIT |
| backend | production | Go | `github.com/jfreymuth/oggvorbis` | `v1.0.5` | MIT |
| backend | production | Go | `github.com/jinzhu/copier` | `v0.4.0` | MIT |
| backend | production | Go | `github.com/joho/godotenv` | `v1.5.1` | MIT |
| backend | production | Go | `github.com/mewkiz/flac` | `v1.0.13` | Unlicense |
| backend | production | Go | `github.com/nicksnyder/go-i18n/v2` | `v2.6.1` | MIT |
| backend | production | Go | `github.com/pkg/errors` | `v0.9.1` | BSD-2-Clause |
| backend | production | Go | `github.com/pquerna/otp` | `v1.5.0` | Apache-2.0 |
| backend | production | Go | `github.com/samber/hot` | `v0.11.0` | MIT |
| backend | production | Go | `github.com/samber/lo` | `v1.53.0` | MIT |
| backend | production | Go | `github.com/shirou/gopsutil` | `v3.21.11+incompatible` | BSD-3-Clause |
| backend | production | Go | `github.com/shopspring/decimal` | `v1.4.0` | MIT |
| backend | test | Go | `github.com/stretchr/testify` | `v1.11.1` | MIT |
| backend | production | Go | `github.com/stripe/stripe-go/v81` | `v81.4.0` | MIT |
| backend | production | Go | `github.com/tcolgate/mp3` | `v0.0.0-20170426193717-e79c5a46d300` | MIT |
| backend | production | Go | `github.com/thanhpk/randstr` | `v1.0.6` | MIT |
| backend | production | Go | `github.com/tidwall/gjson` | `v1.19.0` | MIT |
| backend | production | Go | `github.com/tidwall/sjson` | `v1.2.5` | MIT |
| backend | production | Go | `github.com/tiktoken-go/tokenizer` | `v0.6.2` | MIT |
| backend | production | Go | `github.com/waffo-com/waffo-go` | `v1.3.2` | MIT |
| backend | production | Go | `github.com/waffo-com/waffo-pancake-sdk-go` | `v0.3.1` | MIT |
| backend | production | Go | `github.com/yapingcat/gomedia` | `v0.0.0-20240906162731-17feea57090c` | MIT |
| backend | production | Go | `golang.org/x/crypto` | `v0.54.0` | BSD-3-Clause |
| backend | production | Go | `golang.org/x/image` | `v0.44.0` | BSD-3-Clause |
| backend | production | Go | `golang.org/x/net` | `v0.57.0` | BSD-3-Clause |
| backend | production | Go | `golang.org/x/sync` | `v0.22.0` | BSD-3-Clause |
| backend | production | Go | `golang.org/x/sys` | `v0.47.0` | BSD-3-Clause |
| backend | production | Go | `golang.org/x/text` | `v0.40.0` | BSD-3-Clause |
| backend | production | Go | `gopkg.in/yaml.v3` | `v3.0.1` | Apache-2.0 OR MIT |
| backend | production | Go | `gorm.io/driver/clickhouse` | `v0.6.0` | MIT |
| backend | production | Go | `gorm.io/driver/mysql` | `v1.4.3` | MIT |
| backend | production | Go | `gorm.io/driver/postgres` | `v1.5.2` | MIT |
| backend | production | Go | `gorm.io/gorm` | `v1.25.2` | MIT |
| electron | development | npm | `cross-env` | `7.0.3` | MIT |
| electron | development | npm | `electron` | `39.8.5` | MIT |
| electron | development | npm | `electron-builder` | `26.15.3` | MIT |
| electron | development | npm | `semver` | `7.8.5` | ISC |
| relaykit | production | Go | `github.com/QuantumNous/new-api/relaykit` | `v0.0.0` | Internal module (covered by project license) |
| web | production | npm | `@base-ui/react` | `1.6.0` | MIT |
| web | production | npm | `@codemirror/lang-markdown` | `6.5.1` | MIT |
| web | production | npm | `@codemirror/language` | `6.12.4` | MIT |
| web | production | npm | `@codemirror/state` | `6.7.1` | MIT |
| web | production | npm | `@codemirror/view` | `6.43.6` | MIT |
| web | production | npm | `@fontsource-variable/lora` | `5.3.0` | OFL-1.1 |
| web | production | npm | `@fontsource-variable/public-sans` | `5.3.0` | OFL-1.1 |
| web | production | npm | `@hookform/resolvers` | `5.4.0` | MIT |
| web | production | npm | `@hugeicons/core-free-icons` | `4.2.2` | MIT |
| web | production | npm | `@hugeicons/react` | `1.1.9` | MIT |
| web | production | npm | `@lezer/highlight` | `1.2.3` | MIT |
| web | production | npm | `@lobehub/icons` | `5.14.0` | MIT |
| web | development | npm | `@rsbuild/core` | `2.1.6` | MIT |
| web | development | npm | `@rsbuild/plugin-react` | `2.1.0` | MIT |
| web | development | npm | `@rsbuild/plugin-tailwindcss` | `2.0.3` | MIT |
| web | production | npm | `@tanstack/react-query` | `5.101.2` | MIT |
| web | development | npm | `@tanstack/react-query-devtools` | `5.101.2` | MIT |
| web | production | npm | `@tanstack/react-router` | `1.170.18` | MIT |
| web | development | npm | `@tanstack/react-router-devtools` | `1.167.0` | MIT |
| web | production | npm | `@tanstack/react-table` | `8.21.3` | MIT |
| web | production | npm | `@tanstack/react-virtual` | `3.14.6` | MIT |
| web | development | npm | `@tanstack/router-plugin` | `1.168.23` | MIT |
| web | development | npm | `@types/node` | `26.1.1` | MIT |
| web | development | npm | `@types/react` | `19.2.17` | MIT |
| web | development | npm | `@types/react-dom` | `19.2.3` | MIT |
| web | development | npm | `@typescript/native-preview` | `7.0.0-dev.20260707.2` | Apache-2.0 |
| web | production | npm | `@visactor/react-vchart` | `2.1.4` | MIT |
| web | production | npm | `@visactor/vchart` | `2.1.4` | MIT |
| web | development | npm | `@xyflow/react` | `12.11.2` | MIT |
| web | production | npm | `ai` | `7.0.31` | Apache-2.0 |
| web | production | npm | `auto-skeleton-react` | `1.0.5` | MIT |
| web | production | npm | `axios` | `1.18.1` | MIT |
| web | production | npm | `class-variance-authority` | `0.7.1` | Apache-2.0 |
| web | production | npm | `clsx` | `2.1.1` | MIT |
| web | production | npm | `cmdk` | `1.1.1` | MIT |
| web | production | npm | `dayjs` | `1.11.21` | MIT |
| web | production | npm | `dompurify` | `3.4.12` | Apache-2.0 OR MPL-2.0 |
| web | development | npm | `embla-carousel-react` | `8.6.0` | MIT |
| web | development | npm | `happy-dom` | `20.11.1` | MIT |
| web | production | npm | `i18next` | `26.3.6` | MIT |
| web | production | npm | `i18next-browser-languagedetector` | `8.2.1` | MIT |
| web | production | npm | `input-otp` | `1.4.2` | MIT |
| web | production | npm | `katex` | `0.17.0` | MIT |
| web | development | npm | `knip` | `6.27.0` | ISC |
| web | production | npm | `lucide-react` | `1.25.0` | ISC |
| web | production | npm | `marked` | `18.0.6` | MIT |
| web | production | npm | `motion` | `12.42.2` | MIT |
| web | production | npm | `nanoid` | `5.1.16` | MIT |
| web | production | npm | `next-themes` | `0.4.6` | MIT |
| web | development | npm | `oxc-parser` | `0.137.0` | MIT |
| web | development | npm | `oxfmt` | `0.57.0` | MIT |
| web | development | npm | `oxlint` | `1.74.0` | MIT |
| web | production | npm | `qrcode.react` | `4.2.0` | ISC |
| web | production | npm | `react` | `19.2.7` | MIT |
| web | production | npm | `react-day-picker` | `10.0.1` | MIT |
| web | production | npm | `react-dom` | `19.2.7` | MIT |
| web | production | npm | `react-hook-form` | `7.82.0` | MIT |
| web | production | npm | `react-i18next` | `17.0.10` | MIT |
| web | production | npm | `react-icons` | `5.7.0` | MIT |
| web | production | npm | `react-resizable-panels` | `4.12.2` | MIT |
| web | production | npm | `react-top-loading-bar` | `3.0.2` | MIT |
| web | production | npm | `recharts` | `3.9.1` | MIT |
| web | development | npm | `shadcn` | `4.13.1` | MIT |
| web | production | npm | `shiki` | `4.3.1` | MIT |
| web | production | npm | `sonner` | `2.0.7` | MIT |
| web | production | npm | `sse.js` | `2.8.0` | Apache-2.0 |
| web | production | npm | `stream-markdown-parser` | `1.1.3` | MIT |
| web | production | npm | `tailwind-merge` | `3.6.0` | MIT |
| web | production | npm | `tailwindcss` | `4.3.3` | MIT |
| web | production | npm | `tokenlens` | `1.3.1` | MIT |
| web | production | npm | `tw-animate-css` | `1.4.0` | MIT |
| web | production | npm | `use-stick-to-bottom` | `1.1.6` | MIT |
| web | production | npm | `vaul` | `1.1.2` | MIT |
| web | production | npm | `yace` | `1.1.0` | MIT |
| web | production | npm | `zod` | `4.4.3` | MIT |
| web | production | npm | `zustand` | `5.0.14` | MIT |

## Deployment service images (separate review)

These images are selected by the Compose files and are not linked into the Go
or web dependency graph. Confirm the exact image terms and embedded component
notices before redistribution; the labels below are triage, not legal advice.

| Service | Image reference | License / status |
|---|---|---|
| PostgreSQL | `postgres:15.18-bookworm@sha256:e8db9bd3e9e1751eb639fb17be53cc6d1b62a322adf75b99e791767a7a16ce69` | PostgreSQL License; verify image contents |
| Redis | `redis:7.4.10-alpine@sha256:e7723ff73d963f5cc6d9c4643ea3d989527a402a319239054e9472a7fb9219a2` | RSALv2 or SSPLv1 for Redis 7.4.x; **VERIFY operational/redistribution terms** |
| MySQL (optional) | `mysql:8.4.10@sha256:8dbcf531a03aade657e181b9cf2f1d1803ce621a1d55610cb44cb531ab7d7db6` | GPL/FOSS-exception terms; **VERIFY Oracle image terms** |
| ClickHouse (optional) | `clickhouse/clickhouse-server:26.3.17.56@sha256:422be85ae7344058369cdd366ac0efea9daa8428b55c9cf50258e83a7d12fcb3` | Apache-2.0 project license; verify image contents |

## License Texts

### Apache-2.0

Apache License
Version 2.0, January 2004
https://www.apache.org/licenses/

Licensed under the Apache License, Version 2.0 (the "License"); you may not
use this file except in compliance with the License. You may obtain a copy of
the License at:

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
License for the specific language governing permissions and limitations under
the License.

### Apache-2.0 OR MIT

Dual-licensed components may be used under Apache-2.0 or MIT. Both standard license texts are included below.

Apache License
Version 2.0, January 2004
https://www.apache.org/licenses/

Licensed under the Apache License, Version 2.0 (the "License"); you may not
use this file except in compliance with the License. You may obtain a copy of
the License at:

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
License for the specific language governing permissions and limitations under
the License.

MIT License

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

### BSD-2-Clause

BSD 2-Clause License

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

### BSD-3-Clause

BSD 3-Clause License

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its contributors
   may be used to endorse or promote products derived from this software
   without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

### ISC

ISC License

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY
AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM
LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR
OTHER TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR
PERFORMANCE OF THIS SOFTWARE.

### MIT

MIT License

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

### MPL-2.0

Mozilla Public License, Version 2.0

The Mozilla Public License 2.0 is a file-level copyleft license. Preserve the
license notice and make the source of modified MPL-covered files available
under the MPL-2.0; larger combined works may be distributed under different
terms when the MPL-covered files remain compliant. The authoritative license
text is available at:
https://www.mozilla.org/en-US/MPL/2.0/

### OFL-1.1

SIL Open Font License 1.1

The font dependency listed under OFL-1.1 is licensed under the SIL Open Font
License, Version 1.1. The full license text is available at:
https://openfontlicense.org/open-font-license-official-text/

When distributing font files, preserve the OFL license text, copyright notices,
and reserved font name restrictions supplied by the upstream font project.

### License status unresolved: `github.com/Calcium-Ion/go-epay v0.0.4`

The [repository metadata](https://github.com/Calcium-Ion/go-epay) currently reports
MIT, but the exact [v0.0.4 tag](https://github.com/Calcium-Ion/go-epay/tree/v0.0.4)
(the tagged commit `d8c8810761402e9de0320c4b7eed3cfd7fa94461`) contains no
`LICENSE`, `COPYING`, or `NOTICE` file; the current default branch does contain a
`LICENSE` file. This publication therefore does not assert a license grant or
redistribution permission for the resolved tag. Obtain and record written
confirmation from the rights holder before distributing a compiled binary or
operating a hosted service that includes this dependency.

### Unlicense

The Unlicense

This is free and unencumbered software released into the public domain.
Anyone is free to copy, modify, publish, use, compile, sell, or distribute
this software, either in source code form or as a compiled binary, for any
purpose, commercial or non-commercial, and by any means.

For more information, please refer to https://unlicense.org/
