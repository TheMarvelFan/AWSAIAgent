# Context from the Frontend:
## AI AWS Architect — frontend context

Handoff for deployment. Everything about the React frontend: what it is, how it
is configured, what it assumes about the API, and the decisions behind it.

Companion documents on the backend side: `API-AND-FRONTEND.md` (the full API
reference) and `Backend changes affecting the frontend` (what moved after it).

---

### 1. What this is

A three-panel web client for an agent that turns plain-language requirements
into AWS architecture proposals, and provisions them through Terraform after an
explicit human approval.

The product argument the UI exists to make: **here is what I would build, here
is the plan, and nothing happens until you approve it.** Every design decision
below serves that, and the most important ones are about making refusals and
destructive changes legible rather than making the happy path pretty.

The backend is Go + Gin + Postgres, with a worker that runs Terraform jobs.

---

### 2. Stack

- **React 19 + TypeScript**, built with **Vite 8**
- **Tailwind CSS v4** via the `@tailwindcss/vite` plugin (not v3 — the config
  syntax differs and v3 will not load `vite.config.ts` as written)
- **No routing library.** Navigation is component state, so there is exactly
  one HTML entry point and no deep links. Any host serving `index.html` works;
  no SPA rewrite rules are needed, though pointing 404s at `index.html` does no
  harm.
- **No state library.** Hooks and prop drilling. The app is small enough.
- Fonts from Google Fonts: **Archivo** (interface) and **IBM Plex Mono**
  (plan output, identifiers, numbers)

```bash
npm install
npm run dev     # pinned to port 5173, strictPort
npm run build   # → dist/
npx tsc --noEmit
```

---

### 3. Environment

#### Frontend (`.env` in the frontend root, `VITE_` prefix required)

| Variable | Default | Purpose |
|---|---|---|
| `VITE_API_BASE` | `http://localhost:8080/v1` | API root |
| `VITE_HEALTH_BASE` | `http://localhost:8080` | Health probes sit outside `/v1` |

**That is the entire list.** Three earlier variables were deleted once the API
began reporting the same facts — if any survive in a `.env`, they do nothing:

- ~~`VITE_STUB_ENGINE`~~ → `GET /catalog` returns `reasoning_engine`
- ~~`VITE_STUB_RUNNER`~~ → `GET /catalog` returns `runner`
- ~~`VITE_UNPROVISIONABLE_TEMPLATES`~~ → per-template `provisionable`

This was deliberate. A build-time flag is a second source of truth for something
the server knows, and a stale one makes the UI lie about which engine produced a
proposal.

#### Backend variables the frontend's behaviour depends on

Not read by the client, but the client hardcodes matching assumptions:

| Variable | Why the frontend cares |
|---|---|
| `CORS_ALLOWED_ORIGINS` | Must contain the frontend's exact origin. See §8 |
| `ACCESS_TOKEN_TTL` | Sets the refresh cadence; the client refreshes at 80% of `expires_at` |
| `AGENT_TIMEOUT` (90s) | Client allows 120s on `POST /messages` |
| `DEPLOY_TEARDOWN_AFTER` (45m) | Drives the countdown; `0` disables teardown and leaves `teardown_after` null |
| `TF_WORKSPACE_TTL` (2h) | Plan and apply must happen inside this window or the plan file is swept |
| `LLM_PROVIDER` / `RUNNER_PROVIDER` | Reported through `/catalog`; see §4 |

---

### 4. Stub versus real — the part that matters most

There are **two independent stubs**, and conflating them is the single easiest
way to misrepresent the system.

### 4.1 The two axes

| | Variable | `stub` means | real value |
|---|---|---|---|
| **Reasoning engine** | `LLM_PROVIDER` | Proposals are deterministic keyword matches against the catalog | `bedrock` |
| **Runner** | `RUNNER_PROVIDER` | Plans and applies are fabricated from the config document; nothing reaches AWS | `terraform` |

They are set independently, so all four combinations are reachable. **The
current state at time of writing is stub engine + terraform runner** — fake
reasoning, real infrastructure — because Bedrock allowlisting has not landed.

That combination is the one nobody guesses, and the banner names it explicitly.

#### 4.2 Why the engine stub is the dangerous one

A simulated plan is **visibly** simulated — no resources appear in the AWS
console, no URL works, nothing bills. A canned proposal is **indistinguishable
from a real one**: same JSON shape, same fields populated, same panel rendering.
`scope`, `confidence`, `blockers`, `out_of_catalog` are all filled either way.

So a stale claim of "real model reasoning" is unfalsifiable from the screen.
That is why the flag moved server-side, and why there is a per-message badge.

#### 4.3 How the frontend reports it

**`GET /catalog`** carries the global facts:

```json
{
  "runner": "terraform",
  "reasoning_engine": "stub",
  "reasoning_model": "stub",
  "templates": [ ... ]
}
```

Read once per page load via `useCatalog()` (module-level cache, shared by three
components). Surfaced as a gold strip above the header by `StubBanner.tsx`:

- stub engine only → "Proposals are canned keyword matches, not model output.
  **Plans and applies are real.**"
- stub runner only → "Plans and applies are simulated and create nothing."
- both → both sentences
- neither → the banner does not render

**`assistant_message.model`** carries per-message truth: `"stub"` or a Bedrock
model id. Rendered as a small label under each assistant reply — gold reading
"canned keyword match, not model output" when it is the stub, muted grey showing
the model id otherwise.

Both have a place. The banner covers a fresh chat with no messages; the
per-message label cannot drift even if the server restarts with a different
provider mid-session.

#### 4.4 Provisionability is a runner property, not a catalog property

Each catalog template carries `provisionable` and `unprovisionable_reason`.
**Under the stub runner everything reads `provisionable: true`**, because the
stub fabricates a plan from the document and never looks for a module. So the
flags alone cannot tell you a template is genuinely buildable — that is what the
top-level `runner` field is for.

The UI handles this in two places:

- `CatalogDialog` shows the reason on any unbuildable template, and adds a note
  at the top when the runner is the stub warning that everything below reads as
  buildable because plans are simulated.
- `ConfigPanel` cross-references each block's `template_id` against the catalog
  and **blocks Plan** when any block cannot be built, listing the reasons.

**That block is a client-side judgement, not a server rule.** The server would
let the plan start. The reasoning is that a three-minute Terraform run ending in
a generator error — or worse, a failure landing after approval — is a bad way to
learn something both sides already knew. It is one `disabled` clause in
`ConfigPanel.tsx` if you want it to warn and let through instead.

At time of writing this never fires: `static-site-s3-cloudfront` was pulled from
the catalog rather than shipped behind an AWS CloudFront account-verification
gate, leaving four verified templates.

---

### 5. File map

```
vite.config.ts            port 5173, strictPort, react + tailwind plugins
src/index.css             Tailwind import, design tokens, theme switching
src/App.tsx               composition root; owns active chat, dialogs, deployment id

src/lib/
  api.ts                  one typed function per endpoint
  auth.tsx                AuthProvider, useAuth, RequireAuth
  client.ts               fetch wrapper: auth header, X-Request-ID, refresh, retry
  errors.ts               ApiError, NetworkError, TimeoutError, user-facing copy
  tokens.ts               localStorage token store, refresh timing, cross-tab watch
  types.ts                every API type, hand-maintained (no OpenAPI spec)
  labels.ts               parameter and template naming, shared by panel and diff
  diffReadout.ts          JSON Pointer changes → plain language
  planReadout.ts          Terraform plan text → structured resource list
  paramSpec.ts            catalog parameter specs → form controls; 422 path mapping

src/hooks/
  useCatalog.ts           cached /catalog fetch; runner + engine + template lookup
  useChatList.ts          sidebar list, create, archive, unarchive, update
  useChatSession.ts       one open chat: transcript, config, send turn, budget_change
  useConnection.ts        AWS connection state with a real `checking` phase
  useDeployment.ts        polls while in flight, stops on anything settled
  useVersions.ts          version list, selected version document, diff, revert

src/components/
  AppFrame.tsx            three-region frame, top bar, resizable boundaries
  StubBanner.tsx          stub warnings, read from /catalog
  AuthScreen.tsx          sign in / sign up
  Padlock.tsx             four-state AWS connection indicator
  ConnectionDialogs.tsx   connect flow and disconnect warnings
  ChatHistory.tsx         sidebar
  ConversationHeader.tsx  per-chat header + ArchiveDialog
  ChatSettings.tsx        title and budget
  Conversation.tsx        transcript, pending turn, message box, disclosure
  BudgetConfirm.tsx       budget_change confirmation
  ConfigPanel.tsx         proposal / history / edit; the Apply gate
  ConfigEditor.tsx        direct edits, catalog-driven controls
  VersionHistory.tsx      version list and detail
  DiffView.tsx            summary / JSON / text diff
  DeploymentPanel.tsx     approval gate, plan summary, resolve, teardown
  PlanReadout.tsx         Terraform plan, summary / raw
  CatalogDialog.tsx       what can be built
  Shell.tsx               FirstRun, LiveIndicator, ErrorBoundary
  ResizeHandle.tsx        draggable boundaries, persisted sizes
  ThemeToggle.tsx         dark default, light opt-in

src/App.smoke.tsx         THROWAWAY foundation test; delete before deploying
```

**Naming note.** `lib/planReadout.ts` and `components/PlanReadout.tsx` differ
only in case and directory. If the build environment normalises file casing —
which bit this project once already on a WSL/Windows boundary — rename the lib
file to `planParser.ts` and update the one import in `PlanReadout.tsx`.

---

### 6. Architectural decisions

#### 6.1 Auth and tokens

**Refresh tokens live in `localStorage`.** The API sets
`AllowCredentials: false`, which rules out an httpOnly cookie, so the token has
to be somewhere the page can read. localStorage over in-memory because in-memory
means a full re-login on every page reload.

**The cost is accepted and documented, not hidden:** any XSS on this origin can
read a 30-day refresh token. Keep third-party scripts off the origin. This is a
hackathon trade-off, not a secure default.

**The refresh loop is proactive and single-flighted.** It fires at 80% of the
access token's life, not reactively on a 401. Two concurrent refreshes are
tolerated by a 30-second server grace window, but outside it a reused token is
read as theft and **every session for that user is revoked** — including other
tabs. A `storage` listener signs this tab out when a sibling loses its session.

A failed refresh is never retried. On `invalid_token` everything is cleared and
the user goes to login.

#### 6.2 The frame

Three regions from the spec: history left, conversation centre, config and
deployment stacked right. Both sides collapse; all three boundaries drag, with
sizes persisted per boundary.

**The deployment panel is stacked below the config panel, not tabbed beside it.**
Seeing the proposal and the plan Terraform would run in one frame is the whole
pitch. A tab hides one behind the other; a modal hides the conversation that
produced it.

**The top bar is an addition, not in the original sketch.** The AWS connection
padlock is account-level, and the only alternative home was the left rail —
which is hidden exactly when someone is concentrating on a plan.

#### 6.3 Colour

Dark is the default; light is opt-in via `data-theme="light"` on `<html>`.
Tokens are plain custom properties on `:root` with a light override, mapped to
Tailwind utilities through `@theme inline` so they follow the switch rather than
baking a hex at build time.

**Three colours are reserved and appear nowhere else.** Their appearance always
means the same thing: this cannot be undone.

| Meaning | Dark | Light | Treatment |
|---|---|---|---|
| destroy | `#FF4D7D` | `#B0003A` | solid border |
| replace | `#F5C542` | `#8A6100` | dashed border |
| resolve | `#A78BFA` | `#5B3FBF` | left rule |

Add and change carry no signal colour at all, so anything coloured is
consequential.

**The palette is built for red-green colour blindness.** The red is pushed
toward magenta so it carries blue and separates from yellow on the intact
blue-yellow axis; the two are additionally separated by lightness. Colour is
never the only carrier — border treatment differs, and the word is always
spelled out.

**The diff uses a different palette on purpose:** teal for added, blue for
modified, and grey-with-strikethrough for removed. A config change is
reversible, and §7 of the API reference is explicit that reverting does not
touch deployed infrastructure. Spending the destroy red there would dilute it.

The broken padlock shares the destroy hue but desaturated — saturation and
lightness stay readable where hue does not, and the padlock is never adjacent to
a plan summary.

#### 6.4 Guardrails the UI enforces

- **`applicable: false`** disables Apply, shows the blockers, and keeps the
  proposal on screen. This is the product's core moment.
- **`plan_summary.replace` and `.destroy`** get the reserved colours only when
  non-zero, plus a box stating that a replace deletes the resource and its
  contents, with the resource names listed.
- **Version drift**: a plan for v5 while the panel shows v7 is legal — the hash
  binds to the plan, not the version — but the panel says so above Approve.
- **`unknown`** is rendered as uncertainty, not failure. Gold, not red, with the
  three honest actions: plan again, destroy, or resolve.
- **Resolve** requires typing `I checked`. The user is asserting they looked in
  the AWS console; a wrong assertion leaves real resources billing.
- **Archived chats are frozen**: no messages, no rename, no budget change, no
  plan, no approve. **Destroy, cancel and resolve stay enabled** — archiving
  must never strand live resources.
- **Budget loosening requires confirmation.** A tightening applies immediately;
  raising or removing a ceiling shows the quoted phrase it was read from and
  needs an explicit `PATCH`. The server never raises a ceiling on model output
  alone.

#### 6.5 Readability layers

Three places translate machine output for a non-expert reader, each keeping the
original one click away:

- **Plan readout** — Terraform plan text parsed into resources grouped by
  module, with Summary / Raw tabs. Parsing is approximate; when it finds
  nothing, the toggle disappears and raw is all you get.
- **Diff view** — RFC 6901 pointers rendered as "Memory: 512 MB → 1024 MB",
  with Summary / JSON / Text tabs. v1's add-at-root is special-cased to a block
  list rather than pretending it is a change.
- **Parameter labels** — `memory_mb: 512` reads "Memory / 512 MB". Shared
  between the config panel, the diff and the editor so they agree.

---

### 7. API assumptions worth re-checking after deploy

- Serialisation is uniform: nullable fields are `null`, slices are `[]`, nothing
  is omitted. **Except `GET /catalog`**, which still omits empty `Template` and
  `Parameter` fields because those types are marshalled into the model's system
  prompt. `CatalogTemplate` is typed loosely and read defensively for that
  reason.
- Validation errors are keyed by JSON field name with human-readable messages;
  `body` remains the bucket for "no single field to blame". The client keeps a
  form-level bucket for `body` and any key it does not recognise.
- `confidence` is `number | null`, null for manual edits and reverts to them.
  **Branch on null, never on 0** — a real 0 from a model is a meaningful claim.
  `scope` on a manual version is a default rather than a judgement, so both are
  hidden when `source === 'manual'`.
- Deployments are polled at 4s while in flight; polling stops on anything
  settled. There is no push channel.
- `POST /messages` is synchronous and unstreamable, with no cancel. The client
  shows elapsed seconds rather than a spinner.

---

### 8. Deployment specifics

#### 8.1 The failure that will cost the most time

**An HTTPS page cannot call `http://localhost:8080`.** The browser blocks it as
mixed content, before CORS, with no useful console error. So the frontend and
the API go up together or neither does — deploying only the frontend produces a
dead app.

Recording the demo against localhost is entirely normal and avoids all of this.

#### 8.2 CORS

Three settings constrain a browser client, and a mismatch fails **before your
code runs** with nothing readable:

- `AllowOrigins` must contain the deployed origin **exactly** — scheme, host and
  port. `https://example.com` and `https://www.example.com` are different
  origins.
- `AllowCredentials: false` — cookies do not work. Tokens travel in the
  `Authorization` header.
- `AllowHeaders` is `Authorization`, `Content-Type`, `X-Request-ID`. A custom
  header outside that set is rejected at preflight.
- `ExposeHeaders` is `X-Request-ID` — this is why JavaScript can read it at all.
- `MaxAge` is 12 hours, so **a CORS config change needs a hard refresh** to take
  effect in an already-open browser.
- `AllowMethods` has no `PUT`. The client uses `PATCH`.

#### 8.3 Order of operations

1. Deploy the API, get its public HTTPS URL
2. Set `VITE_API_BASE` and `VITE_HEALTH_BASE` to it
3. Add the frontend's origin to `CORS_ALLOWED_ORIGINS` and restart the API
4. `npm run build`, deploy `dist/`
5. **Load the deployed page and sign in before doing anything else** — a
   successful login proves TLS, CORS and the base URL all at once

#### 8.4 Hosting

Open decision. Amplify or S3 + CloudFront; both Free Tier friendly. Amplify is
fewer steps from a standing start. With S3 + CloudFront, set `index.html` as the
default root object.

No server-side rendering, no API routes, no environment secrets in the bundle —
`VITE_API_BASE` is a public URL and nothing else is embedded.

#### 8.5 Before recording

- **Delete `src/App.smoke.tsx`** and make sure `main.tsx` imports `./App`.
- **Seed two or three chats**, at least one with a deployment history. An empty
  sidebar in the first frame reads as unfinished even when nothing is wrong.
- **Plan and apply within two hours of each other** — `TF_WORKSPACE_TTL` sweeps
  the plan file after that and the apply fails on something unrelated.
- Leave `DEPLOY_TEARDOWN_AFTER` at 45m. Trigger Destroy manually and cut the
  wait in the edit rather than shortening it and risking resources vanishing
  mid-take.
- Consider `ACCESS_TOKEN_TTL=2m` during development so the refresh loop is
  actually exercised; it also shrinks the post-logout window from §5.3.

---

### 9. State of testing

**Verified working:** auth including proactive refresh and cross-tab sync; the
AWS connect flow end to end; chat, proposals, config panel; the budget guardrail
producing `applicable: false` with Apply disabled; version history, diffs and
revert; `stale_config` between two tabs; light and dark across every panel;
panel collapse and resize; the plan reaching `awaiting_approval` with real
Terraform output.

**Built but never exercised:**

- `unknown` status and the resolve dialogue — needs a server killed mid-apply
- Job retry rendering ("attempt 2 of 3") — needs a throttled AWS call
- Terraform `replace` counts — needs an immutable attribute to change between
  two versions, which the stub engine cannot produce
- The config editor against a live server — written against the documented
  contract, not yet run

The first three are the most likely places for a bug to hide.

---

### 10. Open items

- **Hosting** is undecided (§8.4).
- **Bedrock allowlisting** is an open AWS support case. Nothing downstream
  depends on it — `LLM_PROVIDER` is one variable and the stub returns the same
  JSON through the same parsing path — but real output will have longer
  rationales and varying confidence, so do not size those areas to fit the stub.
- **CloudFront account verification** is an open AWS support case.
  `static-site-s3-cloudfront` is pulled from the catalog until it clears; the
  Terraform module exists and works.
- **Connect launch URL region** is hardcoded to `ap-south-1`. The stack only
  creates an IAM role and IAM is global, but the customer must have that region
  enabled — and new-style AWS accounts are pinned to one home region.
  `us-east-1` is the safe default since it can never be disabled.



# Context from the Backend:
## AI AWS Architect — Deployment Handoff

> **Purpose**: everything needed to deploy this system, written for someone who
> has not seen it before.
>
> **Companion documents**: `API-AND-FRONTEND.md` (endpoint reference and UI
> decisions), `FRONTEND-CHANGES.md` (recent API changes), `DECISIONS.md`
> (architecture reasoning). This one covers running it.
>
> **Read §3 before choosing a hosting target.** The Terraform runner has
> constraints that rule out several obvious choices, and discovering them after
> deploying is expensive.

---

### 1. What this is

A hackathon project: an AI assistant that turns plain-language requirements
into AWS architectures and provisions them, behind a human approval gate.

A user describes what they want in a chat. A reasoning engine selects blocks
from a small pre-vetted catalog and fills in parameters. Server-side code — not
the prompt — validates the result against that catalog. The user approves a
Terraform plan bound to a hash, and only then does anything get created, in the
user's own AWS account via cross-account role assumption. Resources tear
themselves down automatically after a configured interval.

**Go 1.27 + Gin backend, React + TypeScript + Vite frontend, PostgreSQL,
Terraform, Amazon Bedrock.**

```
internal/
├── domain/        models, errors, deployment state machine
├── config/        env loading, boot-time validation
├── store/         postgres: users, tokens, chats, configs, connections, deployments, jobs
├── auth/          argon2id, JWT, rotating refresh tokens
├── catalog/       vetted templates + the validator
├── configdiff/    structural + unified text diff
├── reasoning/     prompt, tool schema, service, STUB LLM
├── bedrock/       Converse API with forced tool use
├── awsconnect/    connect flow, STS verification, role naming
├── awsfail/       AWS error classification
├── runner/        infrastructure execution seam
│   ├── stub.go    STUB RUNNER
│   └── terraform/ real runner + bundled modules
├── deployment/    lifecycle service + background worker
└── httpapi/       router, handlers, middleware, httperr
```

---

### 2. The two provider seams — read this carefully

**There are two independent stubs, controlled by two independent variables.**
They fail differently and matter differently. Conflating them is the single
easiest mistake to make with this system.

| | `LLM_PROVIDER` | `RUNNER_PROVIDER` |
|---|---|---|
| Values | `stub` \| `bedrock` | `stub` \| `terraform` |
| Replaces | the reasoning engine | infrastructure execution |
| Interface | `reasoning.LLM` | `runner.Runner` |
| Stub does | keyword-matches the catalog | fabricates a plan, creates nothing |
| Real does | calls Bedrock Converse | shells out to Terraform |
| Costs money | yes (~$0.03–0.05/turn) | yes (AWS resources) |
| Needs AWS | Bedrock access | a connected customer account |
| **Visibly fake?** | **NO** | yes |

#### Why the LLM stub is the dangerous one

A stub *plan* says "STUB - nothing real" in its output and creates nothing. Its
fakeness is on screen.

A stub *proposal* is a well-formed architecture document that passes the same
validation, produces the same diff, and renders identically to a model-generated
one. **Nothing on screen contradicts a claim that it came from a model.** If a
demo asserts "powered by Claude" while `LLM_PROVIDER=stub`, no observer can tell.

Mitigations already in place:

- Startup logs a `WARN` banner for each active stub.
- `GET /catalog` returns `reasoning_engine`, `reasoning_model` and `runner`.
- Every assistant message carries `model` — `"stub"` or the Bedrock model id.
  This is ground truth **per message** and cannot drift, unlike a build-time
  flag.

**A deployment must surface these.** Do not let a frontend env var be the only
thing claiming which engine is live.

#### What the stubs do NOT weaken

The guardrails are independent of both seams. `catalog.Validate` rejects an
invented template identically whether a model or a keyword matcher produced it.
The approval gate binds to a plan hash regardless of what wrote the config. The
budget ceiling is enforced in Go, not in the prompt.

This is a design property worth stating rather than apologising for: the
security behaviour does not depend on which implementation is behind either
interface.

#### Sensible combinations

| `LLM_PROVIDER` | `RUNNER_PROVIDER` | Use |
|---|---|---|
| `stub` | `stub` | Frontend development. No AWS, no cost, fast |
| `stub` | `terraform` | Testing real provisioning without burning model credits |
| `bedrock` | `stub` | Testing prompts and proposals without creating resources |
| `bedrock` | `terraform` | Production |

---

### 3. Deployment constraints — decide hosting with these in hand

#### 3.1 The Terraform workspace must survive between plan and apply

`Apply` runs the **saved plan file** written by `Plan`. That is what makes the
approval binding real: Terraform itself refuses a saved plan whose state has
moved, so the approved plan and the executed plan are provably the same
artifact.

Consequence: **plan and apply must run on the same machine and the same
volume.**

- A single instance with a persistent disk works.
- Multiple instances need sticky routing or shared storage, neither of which is
  implemented.
- An ephemeral filesystem loses the plan between the two, and the runner
  correctly **refuses** rather than re-planning — re-planning would apply
  something nobody approved.

`TF_WORK_ROOT` defaults to `/var/tmp/ai-aws-architect`. It must be writable and
persistent.

#### 3.2 Runs take minutes, and the worker is in-process

The background worker runs as a goroutine inside the API process
(`WORKER_ENABLED=true`). A single Terraform run can take:

| Operation | Time |
|---|---|
| DynamoDB table, budget | seconds |
| S3 bucket with lifecycle rules | ~60s |
| Lambda + API Gateway stack | 1–2 min |
| CloudFront distribution | **10–15 min** |
| CloudFront destroy | **15–20 min** |
| First plan in a cold workspace | +several min for the provider download |

**This rules out request-scoped hosting.** AWS App Runner, Lambda and anything
that scales to zero or has a request timeout cannot host the worker. The
process must stay alive between HTTP requests.

Viable: ECS Fargate with one task, EC2, or a long-lived container anywhere.

#### 3.3 Disk: ~800 MB per Terraform workspace

Each deployment gets its own workspace containing a full copy of the AWS
provider. Measured at 815 MB.

Mitigations, both needed:

- **Plugin cache.** `plugin_cache_dir` in `~/.terraformrc` (or
  `TF_PLUGIN_CACHE_DIR`) makes Terraform hardlink rather than re-download.
  Without it, every deployment pays the download again.
- **Workspace sweep.** Built in: immediate cleanup after apply/destroy, plus a
  periodic sweep governed by `TF_WORKSPACE_TTL` (2h). Workspaces for
  deployments awaiting approval are protected.

Provision at least 20 GB and monitor it.

#### 3.4 Terraform must be on PATH

`RUNNER_PROVIDER=terraform` shells out to the `terraform` binary.
**Terraform 1.10+** is required — the S3 backend uses `use_lockfile` for native
state locking, which avoids needing a DynamoDB lock table. Older versions reject
the flag.

A container image must install it. `TERRAFORM_BINARY` can point elsewhere.

#### 3.5 Credentials: the process needs its own AWS identity

The backend assumes customer roles. It needs a base identity to assume *from*.

Locally that is `AWS_PROFILE=architect-service`. **In AWS, drop `AWS_PROFILE`
entirely and use a task or instance role** — the SDK default credential chain
picks it up. Never ship static keys.

That identity needs:

```json
{"Version":"2012-10-17","Statement":[
 {"Effect":"Allow","Action":["sts:AssumeRole","sts:GetCallerIdentity"],"Resource":"*"},
 {"Effect":"Allow","Action":["s3:GetObject","s3:PutObject","s3:DeleteObject"],
  "Resource":"arn:aws:s3:::<TF_STATE_BUCKET>/*"},
 {"Effect":"Allow","Action":["s3:ListBucket","s3:GetBucketLocation"],
  "Resource":"arn:aws:s3:::<TF_STATE_BUCKET>"},
 {"Effect":"Allow","Action":["bedrock:InvokeModel","bedrock:InvokeModelWithResponseStream"],
  "Resource":["arn:aws:bedrock:*::foundation-model/*",
              "arn:aws:bedrock:*:<ACCOUNT>:inference-profile/*"]}]}
```

**The state-bucket grant is easy to miss.** Terraform's S3 backend
authenticates with ambient credentials, *not* with the provider's assume-role
block. Without it, `terraform init` fails with a 403 that reads like a state
problem rather than a permissions one.

`AWS_SERVICE_ACCOUNT_ID` must match this identity's account. Startup verifies it
via STS and logs an error on mismatch — a mismatch means every trust policy
handed to a customer names the wrong principal.

#### 3.6 Postgres

Migrations run at boot under a Postgres advisory lock, so multiple instances
starting together is safe (verified). `RUN_MIGRATIONS=false` to disable.

Six migrations: init, aws_connections, budget_and_applicability,
confidence_not_null, deployments, confidence_nullable.

---

### 4. Configuration reference

Defaults are what the binary falls back to when a variable is unset — not
necessarily what `.env.example` ships.

#### HTTP and logging

| Variable | Default | Notes |
|---|---|---|
| `HTTP_ADDR` | `:8080` | |
| `GIN_MODE` | `debug` | Anything unrecognised becomes `release` |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | Use `json` in production |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173` | **Must list the deployed frontend origin exactly**, scheme and port included. Comma-separated. `AllowCredentials` is false, so tokens go in the `Authorization` header and cookies do not work |

#### Database

| Variable | Default | Notes |
|---|---|---|
| `DATABASE_URL` | — | **Required.** `sslmode=require` for RDS |
| `DB_MAX_CONNS` / `DB_MIN_CONNS` | 10 / 1 | |
| `RUN_MIGRATIONS` | `true` | |

#### Auth

| Variable | Default | Notes |
|---|---|---|
| `JWT_SECRET` | — | **Required, ≥32 chars.** Refused at boot otherwise. Generate fresh: `openssl rand -base64 48` |
| `JWT_ISSUER` | `ai-aws-architect` | |
| `ACCESS_TOKEN_TTL` | `15m` | Local dev may use `8h`; keep short in production |
| `REFRESH_TOKEN_TTL` | `720h` | |

#### Reasoning engine

| Variable | Default | Notes |
|---|---|---|
| `LLM_PROVIDER` | `stub` | `stub` \| `bedrock` |
| `AWS_REGION` | `ap-south-1` | Where Bedrock and STS are called |
| `AWS_PROFILE` | *(unset)* | **Leave unset in AWS** — use the task role |
| `BEDROCK_MODEL_ID` | a Claude Sonnet profile | Must be enabled for the account **and** region. Note `global.` profiles route across regions; `apac.`/`us.`/`eu.` are geo-scoped |
| `BEDROCK_MAX_TOKENS` / `BEDROCK_TEMPERATURE` | 4096 / 0.2 | |
| `AGENT_MAX_HISTORY_MESSAGES` | 20 | Minimum 2 |
| `AGENT_TIMEOUT` | `90s` | Clients should allow 120s |

#### Cross-account connection

| Variable | Default | Notes |
|---|---|---|
| `AWS_SERVICE_ACCOUNT_ID` | *(unset)* | Empty **disables the connect flow**; proposals still work. Must match the running identity's account |
| `AWS_SERVICE_ROLE_NAME` | `ai-aws-architect-provisioner` | Named in every generated trust policy |
| `AWS_CONNECT_TEMPLATE_URL` | — | **Must be publicly readable** — the customer's browser fetches it |
| `AWS_CONNECT_STACK_REGION` | `us-east-1` | Only where the CloudFormation stack record lives; the IAM role it creates is global. `us-east-1` because it can never be disabled |
| `AWS_DEFAULT_PROVISION_REGION` | `ap-south-1` | Where customer resources actually go |
| `AWS_CONNECTION_CHECK_TTL` | `60s` | How long a health check is trusted |

#### Infrastructure runner

| Variable | Default | Notes |
|---|---|---|
| `RUNNER_PROVIDER` | `stub` | `stub` \| `terraform` |
| `TERRAFORM_BINARY` | `terraform` | Looked up on PATH |
| `TF_WORK_ROOT` | `/var/tmp/ai-aws-architect` | **Must persist between plan and apply** |
| `TF_STATE_BUCKET` | — | **Required** when `RUNNER_PROVIDER=terraform`. Versioning on, public access blocked |
| `TF_STATE_REGION` | `ap-south-1` | |
| `TF_WORKSPACE_TTL` | `2h` | Disk control |

#### Deployment worker

| Variable | Default | Notes |
|---|---|---|
| `DEPLOY_TEARDOWN_AFTER` | `45m` | Zero disables auto-teardown |
| `DEPLOY_MAX_RUN_TIME` | `20m` | On expiry the outcome is **unknown**, not failed |
| `WORKER_ENABLED` | `true` | False queues jobs that never run |
| `WORKER_POLL_INTERVAL` | `2s` | |
| `JOB_STALE_AFTER` | `25m` | **Must exceed `DEPLOY_MAX_RUN_TIME`** — refused at boot otherwise |

---

### 5. AWS prerequisites

#### 5.1 Service account (where this runs)

1. An AWS account for the service itself.
2. IAM role `ai-aws-architect-provisioner` with the policy from §3.5, trusted
   by the task/instance role the process runs as.
3. Terraform state bucket, versioned, public access blocked.
4. Bedrock model access (see §7 — currently blocked).

#### 5.2 Customer account (where resources go)

The customer runs a one-click CloudFormation stack that creates **one IAM
role** with:

- a **trust policy** naming the service role and requiring an external ID that
  the server generates
- a **permission policy** scoped to exactly the catalog's resource types,
  prefix-scoped to `aiarch-*` wherever AWS allows it

Host `deploy/cloudformation/connect-role.yaml` at a publicly readable URL and
set `AWS_CONNECT_TEMPLATE_URL` to it. The customer's browser fetches it before
they have granted anything, so it cannot be private.

**The service never holds a customer credential.** It stores a role ARN and an
external ID; credentials are minted by `sts:AssumeRole` at the point of use and
expire on their own.

---

### 6. What is verified, and what is not

#### Verified end to end against real AWS

- Auth: signup, login, rotating refresh with theft-cascade detection
- Chat, append-only config versioning, structural and text diffs, revert
- Budget enforcement, catalog validation, out-of-scope handling
- Cross-account connection: CloudFormation stack, STS verification, health checks
- Deployment state machine: plan → approve → apply → supersede
- Job queue: per-chat serialisation, retries, stale reclaim
- **Auto-teardown firing unattended 45 minutes later, across a server restart**
- Failure classification: 46 AWS error codes
- Four Terraform modules **created and destroyed** in a live account:
  `object-storage-bucket`, `dynamodb-table`, `serverless-api-lambda`,
  `cost-budget`
- Migrations idempotent; advisory lock correct across two instances

#### Not verified

- **Bedrock**. The account is not allowlisted for the `bedrock-runtime` API
  (§7). The client code compiles but its response parsing has never run.
- **`static-site-s3-cloudfront`**. The module is written and six of its seven
  resources provisioned successfully; the CloudFront distribution failed on an
  **account verification gate**, not on the code.
- Automated test coverage is thin: `configdiff` and `awsfail` only. Everything
  else was verified manually.

---

### 7. Known blockers, both external

#### Bedrock

AWS account `761774875736` returns `ValidationException: Operation not allowed`
for **every** model via `bedrock-runtime` — Anthropic, Amazon Nova and DeepSeek
alike, as account root with `AdministratorAccess`, in an account not part of any
organisation and otherwise fully functional. The same models respond in the
Bedrock console playground, so the block is on the API surface.

Anthropic access is separately pending: `authorizationStatus: NOT_AUTHORIZED`,
and the in-console use-case form returns "not authorized to perform this
action".

Support case **178959145600840** (Account Activation → Bedrock Allowlisting).

**Impact on deployment: none structural.** `LLM_PROVIDER` is one variable. When
it clears, set it to `bedrock` and restart.

#### CloudFront

```
AccessDenied: Your account must be verified before you can add new CloudFront
resources. To verify your account, please contact AWS Support
```

Same account, added to the same support case.

**Impact**: `static-site-s3-cloudfront` can be proposed and approved but fails
at plan time — *after* the user commits. Either remove the template from the
catalog or ensure the UI blocks it using the `provisionable` flag on
`GET /catalog`.

---

### 8. Not implemented — relevant to exposing this publicly

- **No rate limiting anywhere.** Login, signup, messages and apply are all
  unthrottled.
- **Open signup.** No invite code, email verification or allowlist.
- **No LLM quota.** Nothing caps Bedrock spend per user.
- **No password reset.** A forgotten password is unrecoverable without database
  access.
- **Policy drift undetected.** The connection health check proves assume-role
  works, not that the customer's permission policy still matches the catalog.
- **`upstream_failed` conflates** reasoning-engine and AWS failures; an AWS
  error currently reports a message about the reasoning engine.
- **Terraform state holds infrastructure metadata about customer accounts** in
  the service's bucket.

**Do not expose this to the public internet without at least a signup gate and
rate limiting.** It is a hackathon demo with an open door and a path to
spending money in a connected AWS account.

---

### 9. Deployment checklist

```
[ ] Postgres reachable; DATABASE_URL set with sslmode=require
[ ] JWT_SECRET regenerated (not the development one)
[ ] CORS_ALLOWED_ORIGINS lists the deployed frontend origin exactly
[ ] Terraform 1.10+ installed in the image, on PATH
[ ] TF_WORK_ROOT on a persistent, writable volume, 20 GB+
[ ] Terraform plugin cache configured
[ ] TF_STATE_BUCKET created, versioned, public access blocked
[ ] Task/instance role attached; AWS_PROFILE unset
[ ] AWS_SERVICE_ACCOUNT_ID matches the running identity
[ ] connect-role.yaml hosted publicly; AWS_CONNECT_TEMPLATE_URL set
[ ] JOB_STALE_AFTER > DEPLOY_MAX_RUN_TIME
[ ] Single instance, or sticky routing (plan/apply affinity)
[ ] Long-lived process — NOT App Runner, Lambda or scale-to-zero
[ ] Billing alarm and budget on the service account
[ ] Signup gated if reachable from the internet
[ ] LLM_PROVIDER and RUNNER_PROVIDER deliberately chosen, and the UI says which
```

On a healthy boot the log shows, in order: connected to postgres, migrations
(first boot only), catalog loaded, AWS service identity verified, the runner and
reasoning engine with any stub warnings, the worker started, the route table,
and `http server listening`.

Health probes: `GET /healthz` (liveness, no dependencies — a database blip must
not kill the container) and `GET /readyz` (pings Postgres, 2s timeout).

# Deployment context

Companion to the frontend and backend handoff documents. Those describe what the
system is; this one records what was actually deployed, which decisions were
taken and why, and what went wrong on the way.

Written 20 September 2026, after the first working deployment.

---

## 1. What exists

Account `761774875736`, region `ap-south-1`, everything in the default VPC.

```
                        Internet
                           │
         ALB  aiarch-alb  :80  (HTTP only, no certificate)
         aiarch-alb-459915546.ap-south-1.elb.amazonaws.com
                           │
        ┌──────────────────┴───────────────────┐
  rule 10: /v1/*, /healthz, /readyz       default: /*
        │                                      │
   aiarch-tg-backend                    aiarch-tg-frontend
   health check /healthz                health check /nginx-health
        │                                      │
  ECS service aiarch-backend  :8080     ECS service aiarch-frontend  :80
  Fargate, 1 vCPU / 2 GB                Fargate, 0.25 vCPU / 0.5 GB
  50 GiB ephemeral storage              nginx 1.27 + Vite dist
  Go API + worker + Terraform 1.13.5    desiredCount 1
  desiredCount 1 (must stay 1)
        │
        ├── RDS  aiarch-db · db.t3.micro · gp2 · not publicly accessible
        ├── S3   aiarch-tfstate-761774875736   terraform state, versioned, private
        ├── S3   aiarch-public-761774875736    connect-role.yaml, publicly readable
        └── IAM  aiarch-task-role → sts:AssumeRole into the customer account
```

Both ECS services run in public subnets with `assignPublicIp: ENABLED`. There is
no NAT gateway.

### Provider seams as deployed

| | Value | Consequence |
|---|---|---|
| `LLM_PROVIDER` | `stub` | Proposals are canned keyword matches. Bedrock allowlisting is still an open support case |
| `RUNNER_PROVIDER` | `terraform` | Plans and applies are real and cost money |

Fake reasoning, real infrastructure. The startup log emits a `WARN` for the
engine and nothing for the runner, which is the combination the gold banner
names explicitly.

### Identifiers

| Thing | Value |
|---|---|
| Cluster | `aiarch-cluster` |
| Services | `aiarch-backend`, `aiarch-frontend` |
| ECR repositories | `aiarch-backend`, `aiarch-frontend` |
| Task definitions | `aiarch-backend`, `aiarch-frontend` |
| Task role | `aiarch-task-role` |
| Execution role | `aiarch-ecs-execution-role` |
| CI role | `aiarch-github-actions` |
| Secrets | SSM `/aiarch/DATABASE_URL`, `/aiarch/JWT_SECRET` (SecureString) |
| Log groups | `/ecs/aiarch-backend`, `/ecs/aiarch-frontend`, 7-day retention |
| Security groups | `aiarch-alb-sg`, `aiarch-app-sg`, `aiarch-db-sg` |

### Repository configuration

Secret: `AWS_ACCOUNT_ID`.

Variables: `ALB_DNS`, `TF_STATE_BUCKET`, `CONNECT_TEMPLATE_URL`, `PUBLIC_BUCKET`,
`DEPLOY_FROZEN`, `SMOKE_ENABLED`.

`DEPLOY_FROZEN` and `ALB_DNS` **must** be Variables rather than Secrets: they are
read in `if:` conditions, and the `secrets` context is not available there. A
secret would evaluate to empty and the freeze would silently never engage.

They must also be **Repository** variables, not **Environment** variables. No job
declares `environment:`, so environment-scoped values never reach them.

---

## 2. Decisions

### 2.1 One ALB, both halves, same origin

§8.4 of the frontend handoff left hosting open between Amplify and S3 +
CloudFront. Both are out:

- CloudFront account verification is failing on this account (§7). That kills
  S3 + CloudFront outright and makes Amplify Hosting a gamble, since it is
  CloudFront-backed.
- An ALB is the one HTTP front door this account is definitely permitted to
  create.

Serving the frontend from an nginx task behind the *same* ALB as the API, rather
than from object storage, then resolves two more problems at once. §8.1 (an
HTTPS page cannot call `http://localhost:8080`) and §8.2 (CORS) are both
cross-origin problems. Same origin means the browser sends no preflight and has
nothing to block. `CORS_ALLOWED_ORIGINS` is still set correctly, but it stopped
being load-bearing.

### 2.2 HTTP only, deliberately

ACM will not issue a certificate for an `*.elb.amazonaws.com` name, so a bare
ALB cannot serve HTTPS. That is survivable *only because* both halves share the
origin: an HTTP page calling an HTTP API is consistent, where an HTTPS page
calling an HTTP API is a dead app.

Cost: refresh tokens live in `localStorage` and travel in the clear. Accepted for
a demo on a closed security group. Adding a domain later means an ACM
certificate and a `:443` listener, with no application change.

### 2.3 Relative API base

`VITE_API_BASE=/v1`, `VITE_HEALTH_BASE=` (empty). This required one line in
`src/lib/client.ts:108`:

```diff
- const url = new URL(base + clean);
+ const url = new URL(base + clean, window.location.origin);
```

The single-argument `URL` constructor throws on a relative string. The two-
argument form ignores the base when the first argument is already absolute, so
local development against `http://localhost:8080` is unaffected.

The payoff is that the built image carries no hostname and works behind any
origin — no rebuild if the ALB changes.

`VITE_HEALTH_BASE` must be empty rather than `/`, because `"/" + "/healthz"` is
`//healthz`, which a URL parser reads as protocol-relative and resolves to a
host named `healthz`.

### 2.4 Two services, not two containers in one task

§3.1 requires plan and apply to land on the same machine and the same volume.
`TF_WORK_ROOT` lives on the task's ephemeral storage, so **any backend restart
destroys every saved plan awaiting approval**, and the runner then correctly
refuses to apply rather than re-planning.

Splitting the services means a frontend deploy never touches the backend task.

### 2.5 `minimumHealthyPercent=0, maximumPercent=100`

ECS stops the old backend task before starting the new one. The default
(100/200) briefly runs two tasks, and the ALB round-robins between them — so a
plan created on task A can be approved and routed to task B, which has no plan
file and refuses.

Cost: a 30–90 second gap where `/healthz` returns 503 on every backend deploy.
This is expected, not a fault.

`desiredCount` must stay at 1 for the same reason.

### 2.6 Health check on `/healthz`, never `/readyz`

`/healthz` has no dependencies by design; `/readyz` pings Postgres. Pointing the
ALB at `/readyz` would let a database blip kill a healthy task — taking its
ephemeral storage, and every saved plan file, with it.

### 2.7 ALB idle timeout 180s

The default is 60s. `AGENT_TIMEOUT` is 90s and the client waits 120s. With the
stub engine responses are instant, so the default would hide the problem until
the day Bedrock is switched on and then sever every real turn.

### 2.8 Public subnets, no NAT gateway

The tasks need outbound access to ECR and the AWS APIs. A NAT gateway is roughly
$35/month plus data transfer; public subnets with `assignPublicIp` cost nothing.
Ingress is still controlled — the task security group only accepts traffic from
the ALB security group.

### 2.9 OIDC rather than access keys

No AWS credentials are stored in the repository. The `aiarch-github-actions`
role is assumed via `sts:AssumeRoleWithWebIdentity`, scoped to `main`.

Task definitions carry `${ACCOUNT_ID}`, `${ALB_DNS}` and similar placeholders,
expanded with `envsubst` at deploy time, so the public repository holds no
account-specific values.

### 2.10 SSM Parameter Store over Secrets Manager

Standard parameters are free. Secrets Manager is $0.40 per secret per month for
identical behaviour here.

### 2.11 Security group scoped to known addresses

§8 of the backend handoff is blunt: no rate limiting on login, signup, messages
or apply; open signup with no invite code or email verification; no password
reset; and a path to spending money in a connected AWS account.

The ALB security group is therefore allow-listed by IP rather than open. It is
widened to `0.0.0.0/0` only when someone else needs access, and closed again
afterwards.

---

## 3. What went wrong, and how it was fixed

Recorded because most of these are not obvious from the error message.

### 3.1 RDS `InsufficientDBInstanceCapacity`

```
You can't create a db.t4g.micro database instance because there are no
Availability Zones with sufficient capacity for VPC and storage type : gp3
```

Transient regional capacity, not a quota or a permissions problem. `db.t3.micro`
with `gp2` succeeded immediately. t4g is Graviton and t3 is Intel, so they draw
on separate capacity pools — when one is full the other frequently is not.

**Deployed values are `db.t3.micro` + `gp2`, not the `db.t4g.micro` + `gp3` in
the original runbook.** Both are free-tier eligible.

### 3.2 A 503 from the ALB on the first request

The first `curl /healthz` returned an ALB 503; a second, seconds later, returned
`{"status":"ok"}`. The target was still inside its health-check window — 15s
interval with a healthy threshold of 2 means roughly 30 seconds after
registration, on top of a 90s grace period.

The tell: nginx appends a server signature to its own error pages. There was
none, so the 503 came from the ALB, which returns 503 for exactly one reason —
no healthy target in the group.

**This will recur on every backend deploy.** See §2.5.

### 3.3 `Not authorized to perform sts:AssumeRoleWithWebIdentity`

The longest failure. The trust policy expected:

```
repo:TheMarvelFan/AWSAIAgent:ref:refs/heads/main
```

GitHub actually sent:

```
repo:TheMarvelFan@93257324/AWSAIAgent@1378214357:ref:refs/heads/main
```

GitHub now embeds **immutable identifiers** — the user ID and repository ID —
alongside the names in the `sub` claim. The mismatch occurs before any wildcard,
so even `repo:TheMarvelFan/AWSAIAgent:*` could not match it.

Resolved by matching the claim exactly with `StringEquals`, still scoped to
`main`. This is a feature rather than an obstacle: renaming or recreating the
repository now invalidates the trust instead of silently transferring it.

Two things made this slow to diagnose:

- **STS never distinguishes "no such role" from "role exists but does not trust
  you."** Both return the same message, deliberately, so nobody can enumerate
  role names. A malformed ARN and a bad condition look identical from the log.
- **A wrong turn.** The job log header read `GITHUB_TOKEN Permissions: Contents:
  read, Metadata: read` with no `Id Token: write`, which looked like a missing
  permissions block. It was a red herring — the header simply does not list
  `id-token`, and the OIDC token was being minted correctly the whole time. That
  guess cost a full run.

The diagnostic that actually settled it was decoding the claim in the workflow:

```yaml
- name: Show OIDC claims
  run: |
    TOKEN=$(curl -sH "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
      "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=sts.amazonaws.com" | jq -r .value)
    P=$(echo "$TOKEN" | cut -d. -f2)
    while [ $(( ${#P} % 4 )) -ne 0 ]; do P="${P}="; done
    echo "$P" | tr '_-' '/+' | base64 -d | jq '{sub,aud,repository,ref}'
```

Removed after the fix. Worth keeping in mind for any future OIDC setup: read the
claim before editing the policy.

### 3.4 Smoke job timing out for 300 seconds

```
curl: (28) Failed to connect ... port 80 after 300675 ms
```

GitHub's runners are Azure hosts on rotating addresses. The ALB security group
allows one IP. The runners will never reach it.

Gated behind a `SMOKE_ENABLED` repository variable and given `--max-time 15`.
Enabled only while the security group is open to `0.0.0.0/0`.

### 3.5 The frontend calling `http://localhost:8080`

Signup failed with "Could not reach the server". The network tab showed a request
to `http://localhost:8080/v1/auth/signup`.

`.env.production` had been committed to the **repository root** rather than to
`ai-aws-architect-frontend/`. The Docker build context is
`./ai-aws-architect-frontend`, so anything above it is invisible to the builder.
Vite found no `VITE_API_BASE` and `client.ts:11` fell back to its hardcoded
default:

```ts
export const API_BASE = import.meta.env.VITE_API_BASE ?? 'http://localhost:8080/v1';
```

The build log said so plainly — `transferring context: 322.88kB`, the frontend
directory alone — but the page still returned 200, so nothing looked wrong until
a real endpoint was called.

Fixed with `git mv`. Verify a build by checking the served bundle rather than the
source tree:

```bash
JS=$(curl -s http://$ALB_DNS/ | grep -o '/assets/index-[^"]*\.js' | head -1)
curl -s "http://$ALB_DNS$JS" | grep -c 'localhost:8080'   # want 0
```

Note the ordering hazard: committing `.env.production` *without* the
`client.ts:108` patch would produce the same "Could not reach the server"
message but with **no request in the network tab at all**, because `new URL()`
throws and the throw is caught and rethrown as `NetworkError`. The two changes
belong in the same commit.

### 3.6 A second machine timing out

Same root cause as §3.4 — the security group allows a single IP. Not a bug.
Widening it is a deliberate act, given everything in §2.11.

---

## 4. Verified, and not

### Verified in the deployed environment

- Six migrations applied cleanly at boot against RDS with `sslmode=require`
- Catalog loads with **4 templates** — `static-site-s3-cloudfront` is out, so the
  CloudFront verification gate cannot bite after a user approves
- `aws service identity verified` as `assumed-role/aiarch-task-role`, account
  matching `AWS_SERVICE_ACCOUNT_ID`. The task role reaches the SDK default
  credential chain and no `AWS_PROFILE` survived into the image
- `terraform runner ready` with the correct state bucket and work root
- Terraform 1.13.5 present, `/var/tmp/ai-aws-architect` and
  `/var/tmp/tf-plugin-cache` both existing and owned by uid 10001
- ALB routing in both directions: `/healthz` and `/readyz` to the API, `/` to
  nginx, `/v1/catalog` returning 401 (proving the rule reaches the Go service
  rather than nginx's 404)
- Signup and login through the browser against the deployed origin
- Both pipeline jobs building, pushing and rolling out, with path filtering

### Not verified

- **The Terraform runner has never executed.** Everything above is boot-time
  configuration. No `terraform init`, no plan, no apply has run inside this
  container. The plugin cache, workspace behaviour on ephemeral storage, and the
  task role's S3 grants on the state bucket are all configured but unexercised.
  This is where the remaining risk sits.
- The cross-account connect flow against the deployed
  `AWS_CONNECT_TEMPLATE_URL`
- Auto-teardown firing in this environment
- Anything Bedrock. `LLM_PROVIDER` is `stub` and the allowlisting case is open
- The three areas the handoff already flagged as built but never exercised:
  `unknown` status and the resolve dialogue, job retry rendering, Terraform
  `replace` counts

---

## 5. Operational hazards

**Any backend deploy destroys saved plans.** `DEPLOY_FROZEN=true` before
recording or presenting; the backend job skips while it is set. Unset afterwards.

**Never raise `desiredCount` above 1.** Plan and apply must share a machine.

**Plan and apply within two hours.** `TF_WORKSPACE_TTL` sweeps the plan file, and
the apply then fails on something unrelated to what was approved.

**Scaling the backend to zero discards `TF_WORK_ROOT`.** Finish or destroy any
deployment awaiting approval first.

**The stub is invisible on screen.** A canned proposal has the same JSON shape,
the same populated fields and the same rendering as a model-generated one.
Nothing contradicts a claim that it came from a model. The gold banner and the
per-message `model` label are the only things standing between the demo and a
claim that cannot be backed. Leave them visible and say it out loud.

---

## 6. Cost

Running continuously in `ap-south-1`: ALB ~$18/month, backend task ~$36,
frontend task ~$9, 30 GiB of ephemeral storage above the free 20 ~$2, RDS
`db.t3.micro` ~$13 or free inside the account's first twelve months. Roughly
$75–80/month, plus whatever the demo provisions in the connected account.

A billing alarm is live on `EstimatedCharges` at $30, in `us-east-1` — billing
metrics exist only there regardless of where anything runs.

Between sessions:

```bash
aws ecs update-service --cluster aiarch-cluster --service aiarch-backend  --desired-count 0
aws ecs update-service --cluster aiarch-cluster --service aiarch-frontend --desired-count 0
```

The ALB and RDS still bill. Teardown order afterwards: destroy every live
deployment through the UI, scale services to zero, delete services and cluster,
delete the ALB and target groups, delete RDS with `--skip-final-snapshot`, then
the buckets, ECR repositories, IAM roles and the OIDC provider.

---

## 7. If Bedrock clears

Set `LLM_PROVIDER=bedrock` in `deploy/ecs/backend-taskdef.json`, add
`BEDROCK_MODEL_ID` verified against `aws bedrock list-inference-profiles --region
ap-south-1` rather than assumed, and push. The task role already carries
`bedrock:InvokeModel` and `bedrock:InvokeModelWithResponseStream`. The banner
stops warning about the engine on its own, because it reads `/catalog` rather
than a build-time flag.

Size nothing to fit the stub before then. Real output has longer rationales and
varying confidence, and a panel that looks balanced against a canned proposal
will not be.