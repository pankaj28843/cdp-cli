# Authenticated Provider Workflows

`cdp workflow agent` exposes authenticated provider operations through the
signed-in headed Chrome session selected by `--browser-mode headed`.

```bash
cdp --browser-mode headed pages --json
cdp workflow agent providers --json
cdp workflow agent chatgpt capabilities --json
cdp schema webagent-operation --json
```

If the headed `pages` command returns open tabs, that runtime is reachable.
Provider asks use the existing signed-in session directly; they do not require
any provider-wide preflight based on earlier invocations.

## Ask Lifecycle

Every headed ask follows the same small lifecycle:

1. Open one fresh provider tab.
2. Verify the live composer and any explicitly requested mode or model.
3. Insert the exact prompt and optional attachment.
4. Perform one raw Send.
5. Observe the answer and conversation ID.
6. Close only the tab created by this invocation.

Independent asks may run concurrently. The browser-input lease serializes only
the focus-sensitive preparation and Send boundary; it is released before long
answer observation. A vanished process or tab may lose that invocation's
answer, but it does not poison or block the next fresh ask.

The command never closes sibling tabs and never reuses a tab from a previous
ask. It reports whether raw input was attempted, so callers can avoid
duplicating a request after an ambiguous transport failure.

Ask Alex is the one direct-HTTP exception: it resolves its exact course and
chapter context from browser-observed signed-in state, performs one POST, and
returns that response.

## Examples

```bash
printf '%s' 'Review this design.' |
  cdp workflow agent chatgpt ask \
    --stdin \
    --thinking highest \
    --minimum-thinking extra-high \
    --model highest \
    --timeout 40m \
    --json

printf '%s' 'Review this design.' |
  cdp workflow agent claude ask --stdin --json

printf '%s' 'Review this design.' |
  cdp workflow agent gemini ask --stdin --json

printf '%s' 'Review this implementation.' |
  cdp workflow agent grok ask --stdin --json

printf '%s' 'Review this implementation.' |
  cdp workflow agent perplexity ask --stdin --json

printf '%s' 'Critique this itinerary.' |
  cdp workflow agent tripadvisor ask --stdin --json
```

ChatGPT keeps the current thinking and model unless flags or owner-local config
request a selection. `highest` chooses the highest visible option;
`--minimum-thinking` fails before Send if the visible selection is below the
requested floor. Attached files must keep the requested basename and remain
visible at the final Send guard.

## Conversation Reads

Where supported, `list`, `detail`, and `await` are read-only operations over an
observed conversation ID:

```bash
cdp workflow agent chatgpt conversations list --limit 30 --json
cdp workflow agent chatgpt conversations detail <conversation-id> --json
cdp workflow agent chatgpt conversations await <conversation-id> \
  --wait 40m --timeout 40m30s --json
```

Provider-specific `capabilities` output is the executable source of truth for
which read, continue, delete, and auth operations are installed.

When a ChatGPT conversation visibly ends with `Stopped thinking` and its
compose stop control is absent, preserve its exact ID and treat it as terminal.
Consume any assistant answer already present. If `list` or `detail` exposes no
usable answer, record terminal-without-review and do not keep polling,
continue, replace, or reattach it.

For conversations that do not show this terminal UI condition, asynchronously
active detail remains a reason to wait.

## Exact-Target Cleanup

Every live invocation exact-closes the tab it created before returning. Normal
completion reports `cleanup.state=closed`. If Chrome disappears or exact close
cannot be proved, the result reports the exact target ID; it does not prescribe
a follow-up command and it never blocks a later fresh ask.

## Capability Changes

Before an operation becomes supported:

1. focused source tests and compile checks pass;
2. the installed binary exposes matching help and schema;
3. a real authenticated run proves the provider boundary;
4. the invocation exact-closes its own tab without changing sibling tabs.
