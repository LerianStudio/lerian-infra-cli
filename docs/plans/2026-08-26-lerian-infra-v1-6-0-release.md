# lerian-infra v1.6.0 Implementation Plan

> **For implementers:** Use ring-default:executing-plans (rolling-phase: elaborate the
> current phase against the real code, execute its tasks in review-checkpointed
> batches, then elaborate the next phase — repeat),
> ring-default:dispatching-workflows to run each phase as a reviewed multi-agent
> workflow (review + contrarian baked in), or ring-dev-team:running-dev-cycle for the
> full subagent-orchestrated workflow.
> This document is the living source of truth — task elaboration for later
> phases is written back into it during execution.

**Goal:** Publicar a primeira release que contém o binário `lerian-infra` e a
biblioteca `pkg/infra`, com o CLI capaz de obter os próprios templates Terraform.

**Architecture:** O binário e os templates saem da MESMA tag, porque o mapeamento de
chart compilado em `pkg/infra/chartmap.go` e as expressões `helm_values` no HCL são
duas metades de um contrato — o teste que os mantém de acordo roda sobre uma árvore
só. Um checkout que o CLI cria é fixado na tag que casa com o binário, nunca num
branch. Quem quer alvo móvel aponta `--repo` para o próprio clone, e esse fica
intocado. O checkout gerenciado vive em `~/lerian/lerian-terraform-foundation`: não
oculto, porque é um repo git de verdade que o operador deve poder abrir e usar na mão,
e sem versão no caminho, porque a configuração dele (`environments.conf` e os
`envs/*.tfvars`, todos gitignored) mora dentro do checkout e um diretório versionado a
orfanaria em cada upgrade.

**Tech Stack:** Go 1.26, terraform-exec, goreleaser, semantic-release,
GitHub Actions (shared workflows LerianStudio), git CLI.

## Phase Overview

| Phase | Milestone | Epics | Status |
|-------|-----------|-------|--------|
| 1 | De uma máquina sem checkout, `init` oferece e clona a tag certa; todo comando acha o checkout gerenciado; divergência de versão é reportada | 1.1, 1.2, 1.3 | Detailed |
| 2 | `goreleaser release --snapshot` produz binários para linux/darwin/windows localmente, e o workflow de release existe no repo | 2.1, 2.2 | Epic-level |
| 3 | Um recém-chegado sai do zero até a infra no ar lendo só o README | 3.1 | Epic-level |
| 4 | `v1.6.0` publicada com binários na página de release e `pkg/` na tag; o `replace` local do wizard sai | 4.1 | Epic-level |

---

## Phase 1 — Clone gerenciado ligado na CLI

O núcleo já existe e está testado em `pkg/infra/templates.go` (9 testes, rodam sem
rede clonando de um repo bare local). Esta fase é o volante: hoje é motor sem
interface, nada disso aparece para o operador.

### Epic 1.1: Unificar a descoberta do checkout

**Goal:** existe UMA implementação de "isto é um checkout?" e "ache o checkout", na
biblioteca, e o preflight diz de onde os templates estão sendo lidos
**Scope:** `cmd/lerian-infra/main.go`, `pkg/infra/templates.go`
**Dependencies:** none
**Done when:** `cmd/` não define mais marcadores nem walk-up próprios; um comando
rodado de um diretório qualquer com o checkout gerenciado presente resolve; o bloco
`==> Preflight` mostra o caminho e a tag
**Status:** Pending

#### Task 1.1.1: Remover a duplicação de descoberta entre cmd/ e pkg/

- [ ] Done

**Context:** `cmd/lerian-infra/main.go` tem `repoMarkers` (:325), `isRepoRoot` (:331) e
`findRepoRoot` (:343). `pkg/infra/templates.go` passou a ter os equivalentes
`checkoutMarkers` (:263), `IsCheckout` (:269) e `FindCheckout` (:284) — escritos lá
porque o wizard consome a biblioteca e precisa da mesma noção de checkout. Duas cópias
da mesma regra é a semente de divergirem.

**Implementation vision:** A versão da biblioteca é a canônica; apagar as três
declarações de `main.go` e trocar as chamadas por `infra.IsCheckout` /
`infra.FindCheckout`. O comentário longo que hoje explica `repoMarkers` em `main.go:309`
descreve POR QUE os marcadores são esses dois e não um nome bonito — esse raciocínio
tem que migrar para `checkoutMarkers` na biblioteca, não ser perdido. `notACheckout`
(:394) fica em `cmd/`: é texto de interface, não regra.

**Files:**
- Modify: `cmd/lerian-infra/main.go:309-355` (remover), `cmd/lerian-infra/main.go:364-390` (chamadas)
- Modify: `pkg/infra/templates.go` (absorver o comentário dos marcadores)

**Verification:** `go build ./... && go vet ./... && go test ./...` verdes;
`grep -c 'isRepoRoot\|findRepoRoot\|repoMarkers' cmd/lerian-infra/*.go` retorna 0.

**Done when:** existe uma só definição de checkout no projeto, na biblioteca, com o
raciocínio dos marcadores preservado junto dela.

#### Task 1.1.2: Adicionar o checkout gerenciado como passo 4 da resolução

- [ ] Done

**Context:** `resolveLayout` (`cmd/lerian-infra/main.go:364`) resolve em três passos:
`--repo`, `$LERIAN_TF_REPO`, walk-up a partir do cwd. Falhando os três, devolve
`notACheckout` (:394), que hoje lista as três formas de apontar para um checkout.

**Implementation vision:** Inserir um quarto passo, DEPOIS do walk-up: se
`infra.ManagedCheckoutPath(templatesDir)` existir e for um checkout, usar. A ordem
importa — quem está dentro de um checkout de desenvolvimento tem que continuar
dirigindo esse, não o gerenciado. `resolveLayout` passa a receber o override de
`--templates-dir` e a devolver, junto do Layout, se a origem foi o checkout gerenciado
(um `source string` já existe internamente; promover para o retorno). Só o `init` pode
CRIAR o checkout; os comandos de execução apenas leem — um `--action apply` que clona
no meio do caminho é surpresa. `notACheckout` ganha uma quarta linha citando
`lerian-infra init --clone`.

**Files:**
- Modify: `cmd/lerian-infra/main.go:364-405`
- Test: `cmd/lerian-infra/main_test.go`

**Verification:** `go test ./cmd/lerian-infra/ -run Resolve -v`. Casos: checkout
gerenciado existe e cwd está fora de qualquer checkout → resolve para o gerenciado;
cwd DENTRO de um checkout e o gerenciado também existe → resolve para o do cwd;
nenhum dos dois → erro citando as quatro formas.

**Done when:** o binário rodado de `~` acha o checkout gerenciado, e um checkout local
continua ganhando dele.

#### Task 1.1.3: Mostrar a origem dos templates no preflight

- [ ] Done

**Context:** `printPreflight` (`cmd/lerian-infra/main.go:670`) imprime `repo` como
primeira linha (:681). Com duas fontes possíveis para os templates, "qual árvore este
comando está lendo" deixa de ser óbvio, e é a primeira coisa a checar quando um plan
sai diferente do esperado.

**Implementation vision:** Trocar a linha `repo` por `templates`, com caminho, tag e
origem: `~/lerian/lerian-terraform-foundation @ v1.6.0  (managed)` ou
`/path/to/checkout @ v1.5.0  (--repo)`. A tag vem de `infra.InspectCheckout`. Checkout
sem tag (branch, ou commit entre tags) mostra `@ untagged` — nunca inventa versão, que
é a falsa segurança que todo este mecanismo existe para remover. Ler a tag chama `git`
uma vez; se `git` não existir, omitir a tag em vez de falhar, porque um checkout
apontado por `--repo` é utilizável sem git.

**Files:**
- Modify: `cmd/lerian-infra/main.go:670-690`
- Test: `cmd/lerian-infra/main_test.go`

**Verification:** `NO_COLOR=1 go run ./cmd/lerian-infra --env dev --target infra-base
--dry-run` mostra a linha `templates` com caminho e tag.

**Done when:** todo comando diz de qual árvore e de qual tag está lendo.

### Epic 1.2: Oferta de clone no init

**Goal:** numa máquina sem checkout, `init` explica, oferece e clona a tag do binário
**Scope:** `cmd/lerian-infra/init.go`, `cmd/lerian-infra/prompt.go`
**Dependencies:** Epic 1.1
**Done when:** em terminal, `init` sem checkout pergunta e clona; fora de terminal,
falha nomeando a flag; o clone é da tag do binário, não de `main`
**Status:** Pending

#### Task 1.2.1: Flags e resolução do destino do clone

- [ ] Done

**Context:** `initOptions` (`cmd/lerian-infra/init.go:66`) e o bloco de flags (:104-116)
seguem o padrão "toda pergunta tem uma flag", que é o que permite CI nunca cair num
prompt. `runInit` chama `resolveLayout` em :125 e falha ali se não achar checkout.

**Implementation vision:** Três flags novas: `--clone` e `--no-clone` (decisão
explícita) e `--templates-dir` (destino alternativo, para home read-only, home de rede
ou quem organiza ferramentas sob XDG). `--clone` e `--no-clone` juntos é erro, não uma
precedência silenciosa. `runInit` passa a tratar o erro de `resolveLayout` em vez de
propagar: se for "não achei checkout", entra no fluxo de clone; qualquer outro erro
sobe. `--repo` explícito desliga o fluxo de clone inteiro — quem aponta um caminho está
dizendo onde está, não pedindo um download.

**Files:**
- Modify: `cmd/lerian-infra/init.go:66-130`
- Test: `cmd/lerian-infra/init_test.go`

**Verification:** `go test ./cmd/lerian-infra/ -run 'Clone' -v`. Casos: `--clone` e
`--no-clone` juntos erram citando ambas; `--repo` com `--clone` erra explicando que são
mutuamente exclusivos; `--templates-dir` é respeitado.

**Done when:** as três flags existem, se recusam a combinações ambíguas, e o erro de
checkout ausente é distinguível de outros erros.

#### Task 1.2.2: O diálogo de clone, e a recusa fora de terminal

- [ ] Done

**Context:** `prompter.confirm` (`cmd/lerian-infra/prompt.go:98`) já dá o padrão: fora
de TTY devolve erro dizendo que precisa de confirmação e citando `--auto-approve`, e em
TTY dreno o stdin antes de perguntar (`drainStdin`) porque a resposta guarda uma
escrita. `infra.CloneTemplates` (pkg/infra/templates.go) já recusa destino não vazio e
valida que o clonado parece checkout.

**Implementation vision:** Antes de perguntar, imprimir onde procurou e não achou — as
quatro origens, cada uma com o valor que tinha — porque "não achei" sem "procurei aqui"
manda o operador adivinhar. Depois explicar a fixação de versão em uma frase e mostrar
o comando `git clone` literal que será executado; ver o comando é o que torna a
confirmação informada. `--clone` pula a pergunta; `--no-clone` transforma em erro; sem
nenhum dos dois e sem TTY, erro nomeando as duas flags, porque um run de CI nunca deve
clonar por acidente. Durante o clone, o spinner existente (`spinner.go`) com a linha em
`dim`. Depois do clone, `runInit` segue o fluxo normal com o Layout resolvido.

Binário sem ldflag reporta `version == "dev"` (`main.go:32`): aí não há tag para casar.
Clonar `main` e avisar, em `alert` (vermelho e negrito), que a paridade
template↔binário NÃO está garantida — é uma escolha cujas consequências o operador
assume, que é exatamente o critério do `style.alert` (`style.go:45`).

**Files:**
- Modify: `cmd/lerian-infra/init.go`, `cmd/lerian-infra/usage.go`
- Test: `cmd/lerian-infra/init_test.go`

**Verification:** `go test ./cmd/lerian-infra/ -run 'CloneOffer|NonInteractive' -v`. O
teste de TTY usa um repo bare local como origem, no mesmo padrão de
`pkg/infra/templates_test.go:fakeTemplatesRepo`, para não tocar a rede.

**Done when:** máquina sem checkout resolve em uma pergunta; CI falha com instrução; o
caso do binário `dev` avisa em vez de fingir paridade.

### Epic 1.3: Divergência de versão e `--sync`

**Goal:** um binário novo sobre um checkout antigo é detectado e corrigido sem perder a
configuração do operador
**Scope:** `cmd/lerian-infra/init.go`, `cmd/lerian-infra/main.go`
**Dependencies:** Epic 1.1, Epic 1.2
**Done when:** binário v1.7.0 sobre checkout v1.6.0 avisa e indica `--sync`; o sync
troca a tag e preserva `environments.conf` e os tfvars; checkout com arquivo tracked
modificado é recusado nomeando os arquivos
**Status:** Pending

#### Task 1.3.1: Detectar e reportar divergência de versão

- [ ] Done

**Context:** `infra.CheckoutState.AtVersion(wanted)` já existe e trata ref desconhecido
como NÃO-paridade de propósito. O binário conhece a própria versão em `main.go:32`.

**Implementation vision:** Depois de resolver o Layout, comparar. Divergência no
checkout GERENCIADO é aviso com remédio (`init --env <env> --sync`), não bloqueio: o
operador pode ter motivo, e travá-lo no meio de um destroy seria pior que o risco.
Divergência num checkout apontado por `--repo` é uma nota mais fraca ainda — aquele
checkout é dele, e bloquear quebraria o fluxo de quem está desenvolvendo template.
Nos dois casos a mensagem diz o que a divergência causa concretamente: valores de um
produto saindo numa forma em modo shared e em outra em dedicated.

**Files:**
- Modify: `cmd/lerian-infra/main.go` (após resolveLayout), `cmd/lerian-infra/init.go`
- Test: `cmd/lerian-infra/main_test.go`

**Verification:** `go test ./cmd/lerian-infra/ -run Mismatch -v`. Casos: gerenciado
divergente → aviso citando `--sync`; `--repo` divergente → nota, sem `--sync`;
igual → silêncio.

**Done when:** divergência nunca passa calada e nunca bloqueia.

#### Task 1.3.2: `init --sync`

- [ ] Done

**Context:** `infra.SyncTemplates` já faz o trabalho: recusa arquivo tracked
modificado nomeando cada um, depois `fetch --tags --prune` e `checkout --detach`.
Untracked e ignored — que é toda a configuração — sobrevivem por construção, e é isso
que justifica o caminho gerenciado não ter versão.

**Implementation vision:** `--sync` é um modo do `init` que não escreve tfvars: só
move o checkout e sai. Só faz sentido no gerenciado — com `--repo` é erro explicando
que aquele checkout é do operador. Antes de mover, imprimir de/para. Depois, reafirmar
em uma linha que a configuração ficou, porque a dúvida "perdi meus tfvars?" é a
primeira que aparece e responder antes vale mais que responder depois.

**Files:**
- Modify: `cmd/lerian-infra/init.go`, `cmd/lerian-infra/usage.go`
- Test: `cmd/lerian-infra/init_test.go`

**Verification:** `go test ./cmd/lerian-infra/ -run Sync -v`, cobrindo: sync move a
tag e preserva um `environments.conf` escrito antes; sync com template editado é
recusado citando o arquivo; `--sync` com `--repo` é erro.

**Done when:** upgrade de binário é um comando, e ele nunca come a configuração.

---

## Phase 2 — Pipeline de release

### Epic 2.1: `.goreleaser.yml` para o lerian-infra

**Goal:** `goreleaser release --snapshot --clean` produz binários para
linux/darwin/windows em amd64/arm64
**Scope:** `.goreleaser.yml`, `SECURITY.md`
**Dependencies:** Phase 1 (não faz sentido lançar o clone pela metade)
**Done when:** snapshot local gera os arquivos; o binário gerado reporta a tag no
`--version`
**Status:** Pending

*(Sem tasks ainda. Pontos já decididos que a elaboração deve respeitar: o ldflag injeta
`{{.Tag}}` e não `{{.Version}}`, porque o valor é usado como git ref e deve SER o ref —
sem remontar `"v" + version` — e tags de prerelease passam intactas; o alvo do ldflag é
`main.version`, pacote `main`, não um pacote `internal/version` como no lerian-cli; e o
archive NÃO precisa mais carregar `examples/`, porque o clone gerenciado substituiu essa
necessidade.)*

### Epic 2.2: Workflow de release

**Goal:** uma tag publicada dispara o build e anexa os binários à release
**Scope:** `.github/workflows/go-release.yml`
**Dependencies:** Epic 2.1
**Done when:** o workflow existe, aponta para o shared workflow na versão correta, e
`go_version` casa com o `go.mod`
**Status:** Pending

*(Sem tasks ainda. O que já se sabe: o `ci.yml` daqui já tem o job `release` com
semantic-release gated em `needs: [validate, go]`, então o `release.yml` do lerian-cli
NÃO é necessário — o `go-release.yml` engata no `release: published` que este repo já
dispara. `go_version` tem que ser `'1.26'`, não o `'1.25'` do lerian-cli, porque o
`go.mod` daqui é `go 1.26.0`.)*

---

## Phase 3 — Documentação

### Epic 3.1: README com o lerian-infra como caminho primário

**Goal:** um recém-chegado sai de zero até infra no ar lendo só o README
**Scope:** `README.md`
**Dependencies:** Phase 1, Phase 2
**Done when:** o README documenta instalar o binário, o clone dos templates, e o
caminho feliz de um ambiente; e diz quando `deploy.sh` ainda é o caminho
**Status:** Pending

*(Sem tasks ainda. Estado atual medido: o README tem 0 menções a `lerian-infra` e 19 a
`deploy.sh`, ou seja documenta o produto anterior. O `deploy.sh` NÃO sai — continua
sendo o único caminho para os ~10 roots de GCP e Azure —, então a tarefa é dizer qual
usar quando, não apagar um.)*

---

## Phase 4 — Lançamento

### Epic 4.1: Commit, PR e v1.6.0

**Goal:** `v1.6.0` publicada, com binários na página de release e `pkg/` dentro da tag
**Scope:** git, GitHub
**Dependencies:** Phases 1-3
**Done when:** `go get github.com/LerianStudio/lerian-terraform-foundation/pkg/infra@v1.6.0`
funciona de fora do repo, e a página de release tem os binários
**Status:** Pending

*(Sem tasks ainda. Contexto medido: 34 commits na `feat/aws-v2-foundation` à frente de
`origin/main` mais ~45 arquivos não commitados; `git ls-tree v1.5.0` tem ZERO arquivos
em `pkg/` ou `cmd/`, que é a dívida que obriga o wizard a usar `replace` local. Os
commits precisam ser convencionais para o semantic-release cortar minor — `feat:` para
o CLI. Fecha avisando a sessão do wizard que o `replace` pode sair.)*
