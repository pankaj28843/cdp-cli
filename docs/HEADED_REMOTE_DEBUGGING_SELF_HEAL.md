# Headed Chrome remote-debugging self-heal

## Purpose

Headed cdp uses the user's existing Chrome profile so authenticated sessions
remain available. Recent Chrome versions can show a native `Allow remote
debugging?` sheet for each pending DevTools connection. A successful
accessibility action is not enough evidence: the sheet must be gone and the
real CDP transport must become usable.

## Repair contract

The normal path is deliberately not UI automation. Before launching a new
headed default-profile Chrome process, cdp-cli atomically sets Chrome's
persisted `devtools.remote_debugging.user-enabled` preference in `Local State`
when no default-profile Chrome process is using the file. This is the cheap,
deterministic path and is safe to skip when Chrome is already running. The
native approval-sheet adapter is only the fallback for an already-running
profile; it is never treated as proof on its own.

The repair loop is intentionally bounded and narrow:

1. Start or keep the headed daemon transport waiting for the real Chrome CDP
   connection.
2. If the default profile is not running, enable the persisted preference and
   launch one headed default-profile window with the exact inspect page.
3. If the profile is already running and the preference is disabled, inspect
   every Chrome window.
4. In each window, act only on a sheet whose exact title is `Allow remote
   debugging?` and whose exact button is an enabled `Allow` action.
5. Repeat the scan for newly queued sheets. This handles a daemon that is
   still waiting for the connection or that creates a second request after
   the first approval.
6. Stop after the bounded drain. If a sheet remains, report
   `queue_remaining`; never claim repair succeeded.
7. Require the daemon to report a ready RPC socket after its real WebSocket
   connection succeeds. Only then is headed cdp considered healed.

The helper never clicks arbitrary dialogs, buttons, or page content. It also
does not approve prompts for other applications.

On Ubuntu/Linux, the adapter is an embedded, bounded AT-SPI helper. It
whitelists the Google Chrome channel application name, scans the complete
application accessibility tree, and activates only a button named or
described exactly `Allow` under a surface titled exactly `Allow remote
debugging?`. The helper waits at most 12 seconds for the first prompt and
drains at most 20 queued prompts. The Ubuntu package `python3-pyatspi` must be
installed; missing desktop accessibility support returns a structured failed
repair rather than an unverified success.

The scheduled healthy path is deliberately cheaper: when the selected headed
runtime already has a ready RPC socket and a successful target probe, cron
returns `healthy/action=none` without opening Chrome, changing the preference,
or starting another hold. Only an unhealthy runtime enters repair. That repair
owns a 20-second lease, so a wedged process cannot accumulate a permanent lock
or keep an old handle able to remove a later owner's lock.

## Commands

One-shot repair and verification on macOS:

```sh
cdp --browser-mode headed --auto-connect daemon approve --json
```

Keepalive repair with the same bounded self-heal enabled:

```sh
CDP_MACOS_SELF_HEAL_APPROVAL=1 \
  cdp --browser-mode headed --auto-connect daemon keepalive \
  --repair --probe active --macos-self-heal-approval --json
```

The JSON result includes the approval adapter, number of scanned windows,
approval count, remaining prompt count, queue state, and the verified probe.
Successful scheduled ticks additionally report `self_heal_skipped=runtime_healthy`
when no repair was needed.

## Ownership rule for rescue scripts

The headed cdp cron task is the single owner of headed readiness. The dotfiles
Chrome/Edge rescue scripts therefore leave cdp cron entries and cdp daemon
processes active by default. They may still force-stop and reopen a browser,
after which the cdp task repairs the connection.

Use their explicit `--pause-cron` option only when emergency isolation is
needed. This prevents a manual rescue script from silently pausing the
one-minute self-healing job and leaving headed CDP unavailable.

## Failure behavior

If the daemon is still waiting, the next approval may appear immediately after
the previous one. The helper keeps scanning all Chrome windows until the
bounded queue is empty. If Chrome continues generating prompts beyond the
bound, cdp returns `permission_pending` with the remaining count instead of
retrying indefinitely or reporting a false success.

If the queue is empty but the probe is not `cdp_available`, repair remains
pending: the approval action itself did not prove transport health. Inspect
the daemon logs and rerun the bounded repair command after correcting the
underlying endpoint or Chrome state.

## Platform map

The queue-drain and verification contract is shared across platforms.

| Platform | Adapter | Current behavior | Next implementation |
| --- | --- | --- | --- |
| macOS | `Local State` preference path plus native ApplicationServices/Quartz fallback | Implemented; enables the preference before a new default-profile launch, otherwise activates headed Chrome, finds the exact checkbox through AX, posts one Quartz session click, scans all Chrome windows, and drains exact approval sheets. | Keep the CDP probe verification as the gate |
| Ubuntu/Linux | Embedded AT-SPI helper | Scans all whitelisted Chrome application windows, drains only the exact approval sheet within bounded wait/pass limits, then relies on the real CDP probe for success | Keep the installed `python3-pyatspi` prerequisite and rerun live approval/transport proof on each supported Ubuntu desktop image |
| Other desktop platforms | Placeholder | Reports unsupported with a structured remediation result | Add the platform accessibility adapter, then reuse the same shared contract |

This keeps provider and browser transport logic independent from desktop UI
automation. The Linux helper only implements the approval-drain boundary; it
does not duplicate daemon lifecycle or CDP verification code.
