# Authenticated Provider Workflows

`cdp workflow agent` owns authenticated web-agent workflows behind
`webagent-operation/v1`. The executable capability catalog is the source of
truth:

```bash
cdp workflow agent providers --json
cdp workflow agent claude capabilities --json
cdp workflow agent gemini capabilities --json
cdp workflow agent grok capabilities --json
cdp schema webagent-operation --json
cdp describe --command 'workflow agent' --json
```

An operation is callable only when its capability reports `supported=true`.
Planned behavior remains visible but unavailable. Browser-free capability and
doctor commands do not probe Chrome.

## State Boundaries

Three state classes are deliberately separate:

- `webagent/recovery/`: lifecycle phase, exact target/session identity, action
  counts, acknowledgement identity, and cleanup state.
- `webagent/admission/`: provider serialization, spacing, cooldown, mutating
  classification, prior outcome, and exact run identity.
- `webagent/<provider>/`: provider-specific owner-only auth, runtime capability,
  or validated replay state.

Recovery and admission state never contain prompts, answers, cookies, headers,
tokens, request bodies, or raw provider responses. Provider state directories
are `0700`; files and locks are `0600`, reject symlinks, use a same-directory
synced temporary, atomic rename, directory sync, and cross-process locking.
Admission waits through ordinary minimum spacing only within the command's
existing context, then reacquires the lock and rechecks state. Provider
cooldowns remain typed immediate stops and are never waited through or bypassed.
If a process disappears while a mutating admission is active, or releases with
an unknown mutation outcome, admission remains quarantined indefinitely;
elapsed spacing never makes the mutation retryable. Orphaned read-only
operations are safely marked abandoned after the process lock disappears.
Only a provider with a proven stable replay path may retain the minimum private
request template needed for that replay.

Normal JSON output reports only safe readiness, counts, timestamps, stages,
target lifecycle, cleanup proof, and executable next commands.

The browser-wide headed-input lease is separate from those provider state
classes:

```text
<state-dir>/locks/headed-browser-input.lock
```

Any workflow that may activate a target or dispatch input acquires this
owner-only lock before creating its target. A single-Send ask releases it
immediately after the raw-input boundary so answer observation can overlap;
exact target close releases it on every earlier failure. Provider admission
still independently serializes rate and cooldown policy. A foreground command
that meets a just-finished read-only maintenance action waits through the local
minimum-spacing interval and rechecks rather than failing spuriously.

## Claude

Claude currently implements:

```bash
cdp workflow agent claude doctor --json
cdp workflow agent claude auth refresh --json
printf '%s' 'Review this design.' | cdp workflow agent claude ask --stdin --json
cdp workflow agent claude conversations list --limit 30 --json
cdp workflow agent claude conversations detail <conversation-id> --json
cdp workflow agent claude conversations await <conversation-id> --json
cdp workflow agent claude conversations delete <conversation-id> --json
cdp --timeout 3m workflow agent claude calibrate --json
cdp workflow agent claude calibration status --json
cdp --timeout 1m workflow agent claude calibration cleanup --json
```

`doctor` reads owner-only local state and never probes the browser.

`auth refresh` is intent-level and selects the headed runtime unless the caller
explicitly requests an incompatible headless mode. It:

1. acquires Claude admission;
2. checks the headed tab/window budget;
3. creates and attaches exactly one fresh target;
4. enables Network on that exact session and navigates to Claude `/new`;
5. accepts only a successful HTTPS GET whose request and response agree on the
   observed organization/list endpoint;
6. reads current Claude cookies from the same session and requires
   `sessionKey` or `sessionKeyLC`;
7. persists the private validated replay template;
8. records terminal lifecycle state and exact-closes only the owned target.

No prompt is inserted and no raw input action is dispatched. Live acceptance
must compare successful conversation-list fingerprints before and after and
prove they are identical, then prove the headed page-set fingerprint is
unchanged.

The private template is:

```text
~/.cdp-cli/webagent/claude/request-template.json
```

Do not print, copy, commit, or attach it to bug reports.

Each `ask` starts a fresh conversation at `/new`; follow-up turns are not part
of this contract. The exact prompt is prepared and verified before
`action_pending` is persisted and Enter is pressed once. A same-target
`/chat/<id>` route acknowledges the new conversation. Stable detail is tried
first; a 401/403 uses rendered detail on that already-owned target. An
unacknowledged or ambiguous send is never resubmitted.

List, detail, and await first use the observed stable HTTP shape. Claude may
accept the browser session while rejecting direct HTTP context with 401/403.
That one typed outcome lazily receives one fresh exact-owned rendered fallback;
other HTTP failures do not. Await repeats only rendered/incomplete observation
inside that target and never performs browser input. Rendered list fallback
waits for the ordered id/title sequence to remain unchanged across bounded
reads, or returns immediately when the requested limit is reached; it does not
mistake Claude's first partially populated sidebar render for complete history.

Delete navigates one fresh owned target to the exact conversation. Reversible
header/menu preparation may retry. The exact `Delete` confirmation is the only
journaled irreversible click, is dispatched once, and is successful only after
the same target reaches `/new` without the conversation id. A transport-unknown
click may refine to performed only with that persisted postcondition. Every
path exact-closes the owned target.

`calibrate` is an explicit disposable transaction, never part of doctor or auth
refresh. It uses one owned target for a memory-only substantive prompt, one
Send, rendered terminal capture, a journal transition to a second irreversible
action slot, one exact Delete confirmation, the same-target postcondition, and
exact close. Only prompt fingerprint and safe answer counts enter lifecycle
output/state. `calibration status` is browser-free. Explicit `calibration
cleanup` exact-closes only a persisted owned target and deletes only a persisted
acknowledged disposable conversation; it never repeats an ambiguous Send or
Delete.

## Gemini

Gemini currently implements:

```bash
cdp workflow agent gemini capabilities --json
cdp workflow agent gemini auth refresh --json
cdp workflow agent gemini capabilities refresh --json
cdp workflow agent gemini doctor --json
printf '%s' 'Review this design.' | cdp workflow agent gemini ask --stdin --json
cdp workflow agent gemini conversations list --limit 30 --json
cdp workflow agent gemini conversations detail <conversation-id> --json
cdp workflow agent gemini conversations await <conversation-id> --json
cdp workflow agent gemini conversations delete <conversation-id> --json
cdp --timeout 3m workflow agent gemini calibrate --json
cdp workflow agent gemini calibration status --json
cdp --timeout 1m workflow agent gemini calibration cleanup --json
```

The browser-free capability result combines the installed static operation
contract with the last safe runtime observation. `doctor` reads only local auth
and runtime evidence. `auth refresh` owns one fresh headed target, observes the
signed-in surface and session-cookie names, stores only booleans and a
timestamp, submits no prompt, and exact-closes. `capabilities refresh` similarly
observes the unique mode picker, mode labels, upload control, and current
deep-research selection; it does not select a new mode or submit.

Gemini uses rendered headed reads and writes. It does not code or replay the
volatile `batchexecute` transport. Each `ask` requires fresh auth and runtime
evidence, opens `/app` in one fresh owned target, verifies the cached rendered
mode and exact prompt, persists `action_pending`, presses Enter once,
acknowledges the minted same-target conversation route, and captures the
terminal rendered answer. No ambiguous or performed Send is retried.
The headed-input lease is released immediately after that one Enter; route and
answer observation remain bound to the exact owned target.

List opens the sidebar and Recents controls only when needed, then either
reaches the requested limit or progressively advances the unique visible
history scroller until the deduplicated conversation-id set is stable at its
bottom. This prevents a partially hydrated first batch from being treated as a
complete history. Detail and await require the exact conversation route and
read only the rendered prompt/answer. Gemini's visible HTML inserts layout
newlines and removes code indentation, so `innerText` cannot prove exact prompt
identity. Instead, the workflow resolves exactly one visible `Copy prompt`
control inside the last rendered query, temporarily intercepts
`navigator.clipboard.writeText` inside that disposable target, invokes the
control once, and restores the property. It fingerprints the captured prompt
with outer trim only. The observed handler is intercepted before it can write
the system clipboard, ambiguous controls fail closed, and interior or general
whitespace collapsing is not accepted.

Delete navigates one fresh target to the exact conversation, resolves one
strict menu and confirmation control, journals the one irreversible click, and
accepts only the same target reaching `/app` without the conversation id.
Calibration uses one target for one Send, rendered capture, one exact Delete,
the same postcondition, and exact close. Its persisted action slots ensure
cleanup never repeats an ambiguous Send or Delete.

## Grok

Grok currently implements:

```bash
cdp workflow agent grok capabilities --json
cdp workflow agent grok auth refresh --json
cdp workflow agent grok capabilities refresh --json
cdp workflow agent grok doctor --json
printf '%s' 'Review this design.' | cdp workflow agent grok ask --stdin --json
cdp workflow agent grok conversations list --limit 30 --json
cdp workflow agent grok conversations detail <conversation-id> --json
cdp workflow agent grok conversations await <conversation-id> --json
cdp workflow agent grok conversations delete <conversation-id> --json
cdp --timeout 3m workflow agent grok calibrate --json
cdp workflow agent grok calibration status --json
cdp --timeout 1m workflow agent grok calibration cleanup --json
```

`doctor` and the static capability surface are browser-free. Auth refresh owns
one fresh headed target and accepts only the successful signed-in
`GET /rest/app-chat/conversations` request observed on that exact session. It
persists the minimum validated request template in owner-only state, submits no
prompt, and exact-closes. Capability refresh separately observes the stable
`/rest/modes` response and retains only safe mode ids, titles, availability,
selection, and the provider-owned default.

Ask requires fresh auth and runtime evidence. It acquires the shared
headed-input lease before target creation, prepares the fresh composer, and
proves the exact prompt and available default mode. Grok may rerender its editor
during insertion, so reversible select/insert/observe preparation is bounded
and repeatable; the journaled raw Send remains at most once. The same target
must acknowledge `/c/<id>`. Unknown or performed dispatch is never resubmitted.

The canonical answer comes from the browser-observed stable detail sequence:

```text
GET  /rest/app-chat/conversations/<id>/response-node
POST /rest/app-chat/conversations/<id>/load-responses
```

The second request varies only the response ids returned by the first. Detail
accepts only non-empty assistant text with `partial: false` and no stream
errors. List/detail make one fresh exact-owned rendered fallback only after a
typed 401/403 browser-context rejection. Await reads the acknowledged exact id
without browser input. Stored prompt identity normalizes CRLF/CR to LF and
whitespace-only lines to empty because the live provider adds spaces to blank
editor lines. It does not trim, collapse, or otherwise alter any non-empty
line, so code indentation and interior whitespace remain identity-significant.
Rendered layout mismatch cannot override canonical stored identity; the narrow
rendered fallback must independently prove its own exact normalized prompt.

Delete navigates one fresh target to the exact conversation, resolves one
strict visible More menu and role/name `Delete Chat` item, journals the one
irreversible click, and accepts only the same target reaching `/` without the
conversation id. Calibration owns one target for one Send, canonical stored
answer capture, one exact `Delete Chat`, the same postcondition, and exact
close. Separate durable action slots prevent cleanup from repeating either
ambiguous action.

## Recovery

Every browser run has an opaque `evidence.run_id`. Normal completion reports
`cleanup.state=closed`. If exact closure cannot be proven, run only the returned
exact recovery command:

```bash
cdp workflow agent recovery inspect <run-id> --json
cdp workflow agent recovery close <run-id> --json
cdp workflow agent admission status <provider> --json
```

Recovery acts on the one recorded target. It never repeats provider input and
never broadly cleans sibling or user tabs. Exact cleanup does not silently
release a quarantined mutation. After inspecting the result and accepting that
the prior action may already have occurred, a human may explicitly authorize
future new work only when the browserflow record proves cleanup is settled:

```bash
cdp workflow agent admission resolve <provider> <run-id> \
  --acknowledge-unknown --json
```

Resolution requires exact provider/operation/run identity and a closed target
or proof that no target was created. For Ask Alex's direct HTTP replay, which
has no browser target, the resolver instead requires its exact owner-only
pending/performed/unknown action record. Missing, mismatched, or contradictory
evidence fails closed.

## Capability Changes

Before changing an operation from planned to implemented:

1. focused safety, privacy, architecture, and fault checks pass;
2. the exact installed binary exposes matching help/schema;
3. a real authenticated provider run proves usefulness and provider semantics;
4. before/after page sets match;
5. destructive operations prove an exact same-target postcondition;
6. downstream compatibility wrappers and skills are updated together.

Synthetic green tests are guardrails, not completion evidence. A live failure
blocks support and becomes a repaired implementation plus a regression check.
