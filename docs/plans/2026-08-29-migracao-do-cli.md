# Migração do lerian-infra para o repositório próprio — Implementation Plan

> **For implementers:** Use ring-default:executing-plans (rolling-phase: elaborate the
> current phase against the real code, execute its tasks in review-checkpointed
> batches, then elaborate the next phase — repeat),
> ring-default:dispatching-workflows to run each phase as a reviewed multi-agent
> workflow (review + contrarian baked in), or ring-dev-team:running-dev-cycle for the
> full subagent-orchestrated workflow.
> This document is the living source of truth — task elaboration for later
> phases is written back into it during execution.

**Goal:** `cmd/lerian-infra` e `pkg/infra` vivem em `LerianStudio/lerian-infra-cli`,
com histórico preservado, release próprio e uma dependência declarada — não implícita —
dos templates em `lerian-terraform-foundation`.

**Architecture:** Hoje binário e templates saem da mesma tag, e a paridade entre o
mapeamento de chart em Go e o `helm_values` em HCL é garantida por identidade de
versão. Separados, a identidade vira **compatibilidade declarada**: o CLI carrega uma
constante `TemplatesRef` que diz qual tag dos templates ele dirige; `init --clone`
clona esse ref, `--sync` move para ele, e o aviso de divergência compara o checkout com
o ref declarado. É o modelo de skew do kubectl. O ganho que motiva o corte: um breaking
change nos templates deixa de forçar major bump do módulo Go e o import path `/v2` no
wizard. O que se perde — a garantia por construção — é substituído por um teste de CI no
repo do CLI que clona o `TemplatesRef` e lê os `outputs.tf` de verdade, coisa que hoje
não existe (o teste atual compara o Go com um literal, não com o HCL).

**Tech Stack:** Go 1.26, git-filter-repo, goreleaser v2, semantic-release, GitHub
Actions (runners blacksmith), `gh`.

## Phase Overview

| Phase | Milestone | Epics | Status |
|-------|-----------|-------|--------|
| 1 | O código está no repo novo com histórico, compila, testa e gera binários localmente; o README explica o que o repo é | 1.1, 1.2 | Complete |
| 2 | O pin de versão é declarado, não implícito, e um teste de CI prova a compatibilidade contra os templates reais | 2.1, 2.2 | Epic-level |
| 3 | O repo de templates não tem mais Go, seu CI passa, e seu README aponta para o CLI | 3.1 | Epic-level |
| 4 | `lerian-terraform-foundation v1.6.0` e `lerian-infra-cli v1.0.0` publicados; o wizard consome a lib sem `replace` | 4.1, 4.2, 4.3 | Epic-level |

---

## Decisões pendentes que bloqueiam a Fase 4

Registradas aqui para não serem descobertas na hora do release.

1. **O repo `lerian-infra-cli` está PRIVADO** (`gh repo view` em 2026-08-29). Isso quebra
   duas coisas que o README promete: `curl -fsSL https://raw.githubusercontent.com/...
   | sh` devolve 404 para quem não está autenticado, e `go get` do wizard precisa de
   `GOPRIVATE=github.com/LerianStudio/*` mais credencial git na máquina e no CI. Ou o
   repo vira público antes da v1.0.0 — coerente com o repo de templates, que é público e
   Apache-2.0 —, ou o README documenta o caminho autenticado e o `install.sh` deixa de
   ser o caminho principal. **Decidido em 2026-08-29: fica PRIVADO por agora.**
   Consequências: o README do CLI documenta o caminho autenticado; o wizard usa
   `GOPRIVATE=github.com/LerianStudio/*`; o `install.sh` continua funcionando para quem
   tem `gh auth` ou token, e o README diz isso.
2. **Histórico ou recomeço.** `git-filter-repo` não está instalado. Com ele, os ~34
   commits de `cmd/` e `pkg/` vêm com blame; sem ele, é um commit "import from
   lerian-terraform-foundation" e 12.5k linhas sem autoria. A Fase 1 assume filter-repo
   (`brew install git-filter-repo`); o fallback está descrito na Task 1.1.1.

---

## Phase 1 — Código no repo novo, funcionando

### Epic 1.1: Mover o código com histórico e renomear o módulo

**Goal:** `go build ./... && go vet ./... && go test ./...` verdes dentro de
`lerian-infra-cli`, com `git log --follow` mostrando o histórico original de qualquer
arquivo Go
**Scope:** `lerian-infra-cli/` inteiro; leitura de `lerian-terraform-foundation`
**Dependencies:** none
**Done when:** os 170 testes passam no repo novo; `git log --oneline | wc -l` > 1;
nenhum import cita `lerian-terraform-foundation`
**Status:** Done

#### Task 1.1.1: Extrair o histórico de cmd/, pkg/ e dos arquivos de release

- [x] Done

**Context:** O repo de origem está em
`/Users/ferr3ira/Documents/empresas/lerian-studio/projetos/midaz/infrastructure/IAC/TF/lerian-terraform-foundation`,
branch `feat/aws-v2-foundation`, com **129 arquivos não commitados** — todo o trabalho
do CLI desta sessão. `git filter-repo` só vê commits, então a working tree tem que ser
commitada antes de qualquer extração. O destino
`/Users/ferr3ira/Documents/empresas/lerian-studio/projetos/midaz/infrastructure/OPS/lerian-infra-cli`
tem um commit só (`5848c47 Initial commit`) com `LICENSE`, `README.md` e `.gitignore`.

**Implementation vision:** Três passos, nesta ordem.

Primeiro, na origem: commitar tudo que está pendente em UM commit na
`feat/aws-v2-foundation` (`feat: lerian-infra with managed templates and release
pipeline`). Não é o commit final da foundation — é o snapshot que o filter-repo precisa.
Sem ele, o repo novo nasceria com o código de dias atrás.

Segundo, num clone descartável da origem (nunca no checkout de trabalho — filter-repo
reescreve história de forma irreversível): `git filter-repo --path cmd --path pkg
--path go.mod --path go.sum --path .goreleaser.yml --path
.github/workflows/go-release.yml --path SECURITY.md --path scripts/install.sh --path
docs/plans/2026-08-26-lerian-infra-v1-6-0-release.md`. Isso deixa só os commits que
tocaram esses caminhos, com os caminhos intactos.

Terceiro, no destino: adicionar o clone filtrado como remote, `git fetch`, e `git merge
--allow-unrelated-histories` da branch filtrada em `main`. O `.gitignore` vai
conflitar — o do destino é o template Go do GitHub, o da origem tem `bin/` e `dist/`;
resolver mantendo o template do GitHub e acrescentando as duas entradas com os
comentários originais. `LICENSE` não conflita (não estava na lista de paths).

**Fallback sem filter-repo:** `brew install git-filter-repo`. Se não for possível,
copiar os mesmos caminhos com `rsync -a` e commitar como `chore: import lerian-infra
from lerian-terraform-foundation@<sha>`, citando o SHA de origem na mensagem para o
blame ter para onde apontar.

**Files:**
- Modify: `lerian-terraform-foundation` (commit da working tree)
- Create: clone temporário em `/private/tmp/.../scratchpad/foundation-filter`
- Modify: `lerian-infra-cli/.gitignore` (merge)

**Verification:** no destino, `git log --oneline --follow -- pkg/infra/run.go | wc -l`
retorna mais de 1; `ls cmd/lerian-infra pkg/infra go.mod .goreleaser.yml
.github/workflows/go-release.yml SECURITY.md scripts/install.sh` existem todos.

**Done when:** o código está no repo novo com a autoria original visível.

#### Task 1.1.2: Renomear o módulo Go

- [x] Done

**Context:** O `go.mod` declara `module github.com/LerianStudio/lerian-terraform-foundation`.
Oito arquivos em `cmd/lerian-infra` importam
`github.com/LerianStudio/lerian-terraform-foundation/pkg/infra` (`main.go:29`,
`init.go:25`, `templates.go:16`, `checklist.go:9`, `prompt.go:21` e três testes). Há
também strings que citam `lerian-terraform-foundation` e que **NÃO** são import path:
`defaultTemplatesRepoURL` (`pkg/infra/templates.go:30`), o caminho do checkout
gerenciado `~/lerian/lerian-terraform-foundation` (`init.go:70`, `init.go:148`,
`templates.go:42`, `usage.go:35`), e prosa no `usage.go` e no doc de pacote. Essas
continuam corretas — apontam para o repo de templates, que não mudou de nome.

**Implementation vision:** Trocar SÓ a linha `module` e SÓ os imports. A regra que
separa os dois casos: o que vem entre aspas depois de `import (` ou numa linha `"github.com/...`
é path e muda; tudo o mais é referência ao repo de templates e fica. Um `sed` cego em
`lerian-terraform-foundation` quebraria o clone — ele passaria a apontar para um repo que
não tem HCL. Depois do rename, `go mod tidy` para o `go.sum` refletir o módulo novo.

**Files:**
- Modify: `go.mod:1`
- Modify: `cmd/lerian-infra/{main,init,templates,checklist,prompt}.go` (linha de import)
- Modify: os `*_test.go` em `cmd/lerian-infra` que importam `pkg/infra`

**Verification:** `grep -rn '"github.com/LerianStudio/lerian-terraform-foundation' --include='*.go' .`
retorna vazio; `grep -c 'lerian-terraform-foundation' pkg/infra/templates.go` retorna
> 0 (a URL de clone sobreviveu); `go build ./... && go vet ./... && go test ./...`
verdes; `gofmt -l .` vazio.

**Done when:** o módulo se chama `github.com/LerianStudio/lerian-infra-cli` e o CLI
ainda sabe de onde clonar os templates.

#### Task 1.1.3: Tooling de release do repo novo

- [x] Done

**Context:** Vieram do repo de origem: `.goreleaser.yml`, `.github/workflows/go-release.yml`
e `SECURITY.md`. Faltam: `.releaserc` (a origem tem um, em JSON, com `main` + `develop`
prerelease), um `ci.yml` (a origem tem um de 200+ linhas que valida Terraform e roda
Go; aqui só o Go interessa), e um `Makefile` (a origem tem targets `build/test/lint` de
Go e targets `deps/hooks` de npm para commitlint). O `.goreleaser.yml` aponta
`release.github.name: lerian-terraform-foundation` e precisa virar `lerian-infra-cli`.
A description do repo no GitHub tem três espaços em "Helm   charts".

**Implementation vision:**

`.releaserc`: copiar o da origem sem alteração — mesmas branches, mesmo `tagFormat:
v${version}`, mesmos plugins. Uma tag `v1.0.0` sai no primeiro `feat:` em `main`.

`ci.yml`: um workflow novo com dois jobs. `go` — `actions/setup-go` com
`go-version-file: go.mod`, depois `go build ./...`, `go vet ./...`, checagem de `gofmt
-l`, `go test ./... -cover`; é o job `go` do `ci.yml` da origem, copiado. `release` —
`needs: [go]`, `if: github.event_name == 'push'`, o mesmo bloco semantic-release da
origem (app token, checkout com `fetch-depth: 0`, `cycjimmy/semantic-release-action`).
Commitlint por action (`wagoid/commitlint-github-action`), como a origem faz em CI, e
SEM `package.json`/npm: o hook local de commitlint da origem existe para um repo onde
Terraform é a maior parte e não há Go toolchain garantido; aqui todo contribuidor tem
Go, e o CI é a barreira.

`Makefile`: só `build`, `test`, `lint`, copiados da origem; os targets `deps`/`hooks`
de npm ficam de fora pelo motivo acima.

`.goreleaser.yml`: trocar `name: lerian-terraform-foundation` por `lerian-infra-cli`
em `release.github`. Nada mais — `project_name: lerian-infra`, o ldflag `{{.Tag}}` em
`main.version` e a lista de archives continuam corretos.

`gh repo edit LerianStudio/lerian-infra-cli --description` com a description sem os
espaços duplicados.

**Files:**
- Create: `.releaserc`, `.github/workflows/ci.yml`, `Makefile`
- Modify: `.goreleaser.yml` (uma linha)

**Verification:** `goreleaser check` valida; `goreleaser release --snapshot --clean
--skip=publish` produz 5 archives `lerian-infra_*` em `dist/` e
`./dist/lerian-infra_darwin_arm64_v8.0/lerian-infra --version` imprime uma versão;
`make lint test` verdes; `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"`
passa.

**Done when:** um push para `main` com `feat:` cortaria `v1.0.0` e anexaria os
binários — verificável só na Fase 4, mas toda a configuração está no lugar.

### Epic 1.2: README do repo novo

**Goal:** quem chega ao repo entende em dez linhas o que ele é, como instalar, e que
ele depende de outro repo para os templates
**Scope:** `README.md`
**Dependencies:** Epic 1.1
**Done when:** o README tem instalação, a relação com os templates, o uso como
biblioteca, e aponta para o tutorial na foundation em vez de duplicá-lo
**Status:** Done

#### Task 1.2.1: Escrever o README

- [x] Done

**Context:** O README atual é o título e a description do GitHub (337 bytes). A
foundation acabou de ganhar um README de 339 linhas com um tutorial AWS de quatro
passos; os comandos são os mesmos e NÃO devem ser copiados para cá — duas cópias de um
tutorial divergem. O nome do repo termina em `-cli`, o que esconde que metade dele é a
biblioteca que o wizard importa; a primeira linha tem que corrigir isso.

**Implementation vision:** Seis blocos, curtos.

1. **O que é**, primeira frase: "CLI **e biblioteca Go**". Segunda: dirige os templates
   de `lerian-terraform-foundation`. Terceira, honesta: hoje cobre AWS/Terraform; GCP,
   Azure e CloudFormation são a direção, não o estado — a description do GitHub
   descreve o destino, e o README não pode deixar alguém abrir issue de "GCP não
   funciona".
2. **Instalar**: o `curl | sh` com a URL nova
   (`raw.githubusercontent.com/LerianStudio/lerian-infra-cli/main/scripts/install.sh`),
   as duas variáveis (`LERIAN_INFRA_VERSION`, `INSTALL_DIR`), o caminho manual pela
   releases page, e `go install github.com/LerianStudio/lerian-infra-cli/cmd/lerian-infra@latest`.
   Pré-requisitos `terraform`, `aws`, `git`; `kubectl` só depois, e o CLI nunca o chama.
   Se o repo ficar privado (decisão pendente), este bloco muda — anotar.
3. **Templates**: uma versão do CLI dirige uma tag declarada dos templates
   (`TemplatesRef`, Fase 2); `init --clone` busca essa tag; `--sync` move para ela;
   todo comando imprime qual checkout e tag está lendo. Link para o tutorial na
   foundation com a frase "o passo a passo de subir um ambiente está lá".
4. **Como biblioteca**: o import path, e um parágrafo dizendo o que `pkg/infra` expõe
   — `Discover`, `Resolve`, `NewRunner`, `Progress` como interface que não imprime,
   `CollectHelmValuesFrom`. Um snippet de 8-10 linhas mostrando `Discover → Resolve →
   NewRunner → Execute`, porque é o contrato que o wizard consome e prosa não fixa uma
   assinatura.
5. **Desenvolvimento**: `make build test lint`; que `lint` é `gofmt` + `go vet` e nada
   mais, igual ao CI.
6. **Segurança e licença**: link para `SECURITY.md`, Apache-2.0.

**Files:**
- Modify: `README.md`

**Verification:** `grep -c 'lerian-terraform-foundation' README.md` > 0 (aponta para
os templates); `grep -c 'biblioteca\|library' README.md` > 0 na primeira linha; nenhum
comando do tutorial da foundation (`--target bootstrap --action apply` etc.) aparece
aqui.

**Done when:** o README não duplica a foundation e não promete GCP/Azure como pronto.

---

## Phase 2 — Compatibilidade declarada

### Epic 2.1: `TemplatesRef` substitui a identidade de versão

**Goal:** o CLI declara qual tag dos templates dirige, e todo caminho que hoje usa
`version` como ref git passa a usar a constante
**Scope:** `pkg/infra/templates.go`, `cmd/lerian-infra/templates.go`,
`cmd/lerian-infra/usage.go`, testes correspondentes
**Dependencies:** Epic 1.1
**Done when:** `init --clone` clona `TemplatesRef`; `--sync` move para `TemplatesRef`;
o aviso de divergência compara o checkout com `TemplatesRef`; um binário `dev` clona
`TemplatesRef` em vez de `main`
**Status:** Pending

*(Sem tasks ainda. Fatos para a elaboração: hoje `acquireTemplates` faz `ref :=
version` e, se `version == "dev"`, clona o branch default com um aviso vermelho;
`runSync` recusa binário `dev` porque "não há versão para sincronizar"; o aviso de
mismatch chama `state.AtVersion(version)`. Com a constante, os três passam a ler
`infra.TemplatesRef`, e o caso `dev` MELHORA: um build local sabe qual tag de templates
quer e não precisa mais cair em `main` — o aviso de "paridade não garantida" pode virar
uma nota mais fraca ou sumir. O ldflag `{{.Tag}}` em `main.version` continua, para o
`--version`; `TemplatesRef` é constante no fonte, bumpada por commit quando uma release
do CLI precisar de templates novos. O `--help` e o README precisam dizer "dirige a tag
X dos templates", e o preflight deve mostrar as duas versões lado a lado.)*

### Epic 2.2: Teste de compatibilidade contra os templates reais

**Goal:** o CI do CLI falha quando `TemplatesRef` aponta para uma tag cujo layout ou
contrato de outputs o código não entende
**Scope:** `.github/workflows/ci.yml`, um teste novo em `pkg/infra`
**Dependencies:** Epic 2.1
**Done when:** um job clona `lerian-terraform-foundation@TemplatesRef` e um teste Go,
rodando contra esse checkout, verifica que os marcadores existem e que cada
`_modules/<engine>/outputs.tf` declara os outputs que `ReadFacts` consome
**Status:** Pending

*(Sem tasks ainda. O que já se sabe: hoje NENHUM teste lê HCL — `TestMidazShapeIsTheSameInBothModes`
compara o mapper Go com um literal escrito no próprio teste, então se o HCL divergir
nada pega. Este epic é a primeira vez que a paridade Go↔HCL vira teste de verdade. O
teste deve ser pulado (`t.Skip`) quando a variável de ambiente que aponta para o
checkout não estiver definida, para `go test ./...` local continuar rodando sem rede.
O que ele verifica: `IsCheckout(path)` true; para cada engine em `chartMappers` e em
`secretPayloadProperty`, o `outputs.tf` do módulo correspondente contém `output
"endpoint"`, `"port"`, `"secret_name"`, `"secret_arn"` e o nome de usuário que
`ReadFacts` normaliza para aquele engine.)*

---

## Phase 3 — Limpar o repo de templates

### Epic 3.1: Remover o Go da foundation e apontar para o CLI

**Goal:** `lerian-terraform-foundation` volta a ser Terraform puro, seu CI passa, e seu
README envia quem quer o CLI para o repo certo
**Scope:** `lerian-terraform-foundation`: `cmd/`, `pkg/`, `go.mod`, `go.sum`,
`.goreleaser.yml`, `.github/workflows/{ci,go-release}.yml`, `Makefile`, `.gitignore`,
`SECURITY.md`, `scripts/install.sh`, `README.md`, `docs/plans/`
**Dependencies:** Phase 1 (o código tem que estar salvo no destino antes de sair da
origem)
**Done when:** `git ls-files | grep -E '\.go$|go\.mod'` vazio na foundation; o job
`release` do `ci.yml` tem `needs: [validate]`; `make` não tem targets Go; o README
instala o CLI via link para `lerian-infra-cli`; `terraform fmt -check -recursive
examples` passa
**Status:** Pending

*(Sem tasks ainda. Decisões para a elaboração: `SECURITY.md` NÃO sai inteiro — a parte
sobre account ids, secrets e state é dos templates e fica; a parte sobre o account guard
e os plan files é do CLI e já foi junto. `scripts/install.sh` sai daqui; o README da
foundation passa a linkar o `curl | sh` do repo do CLI em vez de carregar o comando. Os
69 comentários `.tf` que dizem `lerian-infra` continuam corretos — o nome do binário não
mudou. O plano `docs/plans/2026-08-26-lerian-infra-v1-6-0-release.md` já foi para o CLI
na Task 1.1.1; os outros dois planos são da foundation e ficam. `.gitignore` perde
`/bin/` e `dist/`. O `.releaserc` fica — a foundation continua lançando tags de
templates.*

*Achados da Fase 2 para esta fase: o README da foundation diz em `README.md:96` "The
binary and the templates ship from the same tag" e em `:102` "clones the matching
tag" — ambos falsos agora que o CLI declara o ref; reescrever para "o CLI declara qual
tag dirige". E `.gitignore:77` tem `/docs/`, que é por que nenhum plano foi commitado
na foundation; decidir se `docs/` passa a ser tracked.)*

---

## Phase 4 — Release

### Epic 4.1: `lerian-terraform-foundation v1.6.0`

**Goal:** a primeira tag com o layout v2 existe, sem Go dentro
**Scope:** PR da `feat/aws-v2-foundation` para `main`
**Dependencies:** Phase 3
**Done when:** `git ls-tree v1.6.0 | grep -c 'cmd\|pkg\|go.mod'` retorna 0; `git
ls-tree -r v1.6.0 examples/aws/_modules | wc -l` > 0
**Status:** Pending

*(Sem tasks ainda. Esta tag é o `TemplatesRef` da v1.0.0 do CLI, então tem que sair
PRIMEIRO. O `init --clone` de um binário v1.0.0 vai clonar exatamente esta tag.)*

### Epic 4.2: `lerian-infra-cli v1.0.0`

**Goal:** binários na releases page, `TemplatesRef = "v1.6.0"`, e o `curl | sh` do
README funciona
**Scope:** `pkg/infra/templates.go` (a constante), PR para `main`
**Dependencies:** Epic 4.1, decisão de visibilidade
**Done when:** `curl -fsSL .../lerian-infra-cli/main/scripts/install.sh | sh` instala
um binário que reporta `v1.0.0`; `lerian-infra init --env dev --clone` numa máquina sem
checkout clona a `v1.6.0` e passa em `IsCheckout`
**Status:** Pending

### Epic 4.3: O wizard troca o `replace`

**Goal:** o wizard consome `github.com/LerianStudio/lerian-infra-cli/pkg/infra@v1.0.0`
de um módulo publicado
**Scope:** worktree `lerian-wizzard-wizard-infra-provisioning`: `go.mod` e 25 arquivos
`.go`
**Dependencies:** Epic 4.2
**Done when:** `grep -rn 'lerian-terraform-foundation' --include='*.go' .` vazio no
wizard; `go.mod` sem `replace`; `go build ./...` verde
**Status:** Pending

*(Sem tasks ainda. É `sed` do prefixo do import em 25 arquivos mais `go get
github.com/LerianStudio/lerian-infra-cli@v1.0.0` e a remoção da linha `replace`
(`go.mod:101`). Se o repo do CLI ficar privado, o wizard precisa de `GOPRIVATE` e de
credencial git no CI — ver "Decisões pendentes".)*
