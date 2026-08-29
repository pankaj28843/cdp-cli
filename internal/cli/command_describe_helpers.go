package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func describeCommand(cmd *cobra.Command) commandInfo {
	info := commandInfo{
		Name:     cmd.Name(),
		Use:      cmd.UseLine(),
		Short:    cmd.Short,
		Aliases:  cmd.Aliases,
		Examples: commandExamples(cmd.CommandPath()),
		Flags:    commandFlags(cmd),
	}

	for _, child := range cmd.Commands() {
		if child.Hidden {
			continue
		}
		info.Children = append(info.Children, describeCommand(child))
	}

	return info
}

func commandFlags(cmd *cobra.Command) []flagInfo {
	flags := []flagInfo{}
	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}
		flags = append(flags, flagInfo{
			Name:      flag.Name,
			Shorthand: flag.Shorthand,
			Type:      flag.Value.Type(),
			Default:   flag.DefValue,
			Usage:     flag.Usage,
		})
	})
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].Name < flags[j].Name
	})
	return flags
}

func commandExamples(path string) []string {
	examples := map[string][]string{
		"cdp": {
			"cdp doctor --json",
			"cdp describe --json | jq '.commands.children | map(.name)'",
		},
		"cdp version": {
			"cdp version --json",
			"cdp version --json --compact",
			"cdp version --json | jq --arg head \"$(git rev-parse HEAD)\" '.verified and .commit == $head'",
		},
		"cdp guide": {
			"cdp guide",
			"cdp guide --path",
			"cdp guide --json --jq '.path // .content'",
		},
		"cdp describe": {
			"cdp describe --json",
			"cdp describe --command 'daemon status' --json",
		},
		"cdp doctor": {
			"cdp doctor --json",
			"cdp doctor --check daemon --json",
			"cdp doctor --check scheduled-tasks --json",
			"cdp doctor --check browser-health --json",
			"cdp doctor --check browser-budget --json",
			"cdp doctor --check headless-security --json",
		},
		"cdp explain-error": {
			"cdp explain-error not_implemented --json",
		},
		"cdp exit-codes": {
			"cdp exit-codes --json",
		},
		"cdp schema": {
			"cdp schema --json",
			"cdp schema error-envelope --json",
		},
		"cdp daemon start": {
			"cdp daemon start --auto-connect --json # human-managed: requires Chrome Allow prompt when permission is pending",
			"cdp daemon start --browser-url <browser-url> --json",
			"cdp daemon start --autoConnect --json",
		},
		"cdp daemon approve": {
			"cdp --browser-mode headed --auto-connect daemon approve --json",
			"CDP_MACOS_SELF_HEAL_APPROVAL=1 cdp --browser-mode headed --auto-connect daemon keepalive --repair --probe active --macos-self-heal-approval --json",
		},
		"cdp daemon status": {
			"cdp daemon status --json",
			"cdp daemon health --json",
		},
		"cdp daemon stop": {
			"cdp daemon stop --json",
			"cdp --browser-mode headless daemon stop --force-managed --stale-lock-after 10m --json",
		},
		"cdp daemon restart": {
			"cdp daemon restart --auto-connect --json",
			"cdp daemon restart --debug --autoConnect --active-browser-probe --json",
			"cdp daemon restart --browser-url <browser-url> --json",
			"cdp --browser-mode headless daemon restart --force-managed --stale-lock-after 10m --json",
		},
		"cdp daemon keepalive": {
			"cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe passive --reconnect 30s --display :0 --json",
			"cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe active --reconnect 30s --display :0 --json",
			"CDP_MACOS_SELF_HEAL_APPROVAL=1 cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe active --macos-self-heal-approval --reconnect 30s --json",
			"cdp --browser-mode headed daemon keepalive --auto-connect --repair --json | jq '{state,environment}'",
			"cdp --browser-mode headless daemon keepalive --repair --reconnect 30s --json",
			"cdp --browser-mode headless daemon keepalive --repair --force --json",
			"cdp cron install --json",
			"cdp daemon keepalive --browser-url <browser-url> --json",
			"cdp --connection default daemon keepalive --probe auto --json",
		},
		"cdp daemon maintenance": {
			"cdp --browser-mode headless daemon maintenance --dry-run --json",
			"cdp --browser-mode headless daemon maintenance --json",
			"cdp --browser-mode headless daemon maintenance --json | jq '{state,environment}'",
			"cdp --browser-mode headless daemon maintenance --profile-seed-strategy copy-default --profile-seed-if-older-than 6h --dry-run --json",
			"cdp --browser-mode headless daemon maintenance --stale-lock-after 1s --json",
		},
		"cdp daemon health-check": {
			"cdp --browser-mode headless daemon health-check --repair --json",
			"cdp --browser-mode headless daemon health-check --repair --force --json",
			"cdp --browser-mode headless daemon health-check --require-healthy --json",
			"cdp --browser-mode headless daemon health-check --repair --failure-threshold 1 --json",
			"cdp --browser-mode headless daemon health-check --out-dir tmp/headless-health --json",
		},
		"cdp daemon logs": {
			"cdp daemon logs --tail 100 --json",
			"cdp daemon logs --tail 0 --json",
		},
		"cdp cron": {
			"cdp cron status --json",
			"cdp cron install --json",
			"cdp cron migrate pages-polling --json",
		},
		"cdp cron status": {
			"cdp cron status --json",
			"cdp cron status --json | jq '{state,tasks,managed_processes,last_run_artifacts}'",
			"cdp cron diff --json",
		},
		"cdp cron install": {
			"cdp cron install --json",
			"cdp --browser-mode headed cron install --dry-run --json",
			"cdp --browser-mode headless cron install --dry-run --json",
			"cdp --config cdp.json cron install --dry-run --json",
			"cdp cron install --artifact-retention 168h --max-log-size 64MiB --dry-run --json",
			"cdp cron install --cdp-bin $HOME/.local/bin/cdp --json",
		},
		"cdp cron run": {
			"cdp cron run headed-daemon-keepalive --json",
			"cdp cron run headless-maintenance --json",
			"cdp cron run artifact-prune --json",
		},
		"cdp artifacts": {
			"cdp artifacts prune --dry-run --json",
			"cdp artifacts prune --apply --json",
		},
		"cdp artifacts prune": {
			"cdp artifacts prune --older-than 168h --max-log-size 64MiB --dry-run --json",
			"cdp artifacts prune --older-than 168h --max-log-size 64MiB --apply --json",
		},
		"cdp artifacts run-managed": {
			"cdp artifacts run-managed --task example --log tmp/example.log --max-log-size 1MiB -- echo ok",
		},
		"cdp cron remove": {
			"cdp cron remove --json",
		},
		"cdp cron diff": {
			"cdp cron diff --json",
		},
		"cdp cron migrate": {
			"cdp cron migrate pages-polling --json",
		},
		"cdp cron migrate pages-polling": {
			"cdp cron migrate pages-polling --json",
			"cdp cron migrate pages-polling --apply --json",
		},
		"cdp cron heal": {
			"cdp cron heal headed --json",
		},
		"cdp cron heal headed": {
			"cdp cron heal headed --reconnect 30s --json",
			"cdp cron heal headed --chrome-command google-chrome-stable --json",
		},
		"cdp browser": {
			"cdp browser preflight --json",
			"cdp --browser-mode headless browser preflight --repair --cleanup --json",
			"cdp browser mode get --json",
			"cdp browser profile status --json",
			"cdp browser marker status --json",
			"CDP_BROWSER_MODE=headless cdp browser mode get --json",
		},
		"cdp browser marker": {
			"cdp browser marker enable --name agent --json",
			"cdp browser marker status --json",
			"cdp browser marker disable --json",
		},
		"cdp browser marker enable": {
			"cdp browser marker enable --name agent --json",
			"cdp browser marker enable --json",
		},
		"cdp browser marker disable": {
			"cdp browser marker disable --json",
		},
		"cdp browser marker status": {
			"cdp browser marker status --json",
		},
		"cdp browser preflight": {
			"cdp browser preflight --json",
			"cdp --browser-mode headless browser preflight --repair --json",
			"cdp --browser-mode headless browser preflight --profile-seed managed --repair --json",
			"cdp --browser-mode headless browser preflight --cleanup --include-url example.test --json",
			"cdp --browser-mode headless browser preflight --cleanup --cleanup-close --created-by cdp --json",
			"cdp browser preflight --open-readiness --json",
		},
		"cdp browser mode": {
			"cdp browser mode get --json",
		},
		"cdp browser mode get": {
			"cdp browser mode get --json",
			"cdp --browser-mode headless browser mode get --json",
			"CDP_BROWSER_MODE=headless cdp browser mode get --json",
		},
		"cdp browser profile": {
			"cdp --browser-mode headless browser profile status --json",
			"cdp --browser-mode headless browser profile seed --strategy managed --json",
		},
		"cdp browser profile status": {
			"cdp --browser-mode headless browser profile status --json",
		},
		"cdp browser profile seed": {
			"cdp --browser-mode headless browser profile seed --strategy managed --json",
			"cdp --browser-mode headless browser profile seed --strategy copy-default --json",
			"cdp --browser-mode headless browser profile seed --strategy copy-default --if-older-than 6h --json",
		},
		"cdp connection": {
			"cdp connection list --json",
			"cdp connection current --json",
		},
		"cdp connection add": {
			"cdp connection add local --browser-url <browser-url> --json",
			"cdp connection add default --auto-connect --json",
		},
		"cdp connection select": {
			"cdp connection select local --json",
		},
		"cdp connection current": {
			"cdp connection current --json",
			"cdp --browser-mode headed connection current --json",
		},
		"cdp connection remove": {
			"cdp connection remove stale --json",
		},
		"cdp connection prune": {
			"cdp connection prune --missing-projects --dry-run --json",
		},
		"cdp connection list": {
			"cdp connection list --json",
			"cdp connection list --project . --json",
		},
		"cdp connection resolve": {
			"cdp connection resolve --json",
			"cdp --browser-mode headless connection resolve --connection default --json",
		},
		"cdp targets": {
			"cdp targets --json",
			"cdp targets --retry transient --max-attempts 3 --json",
			"cdp targets --limit 10 --json",
			"cdp targets --type service_worker --json",
		},
		"cdp pages": {
			"cdp pages --json",
			"cdp pages --retry transient --max-attempts 3 --json",
			"cdp pages --limit 10 --json",
			"cdp pages --include-url localhost --exclude-url admin --json",
			"cdp pages --title-contains Example --json",
		},
		"cdp page select": {
			"cdp page select <target-id> --json",
			"cdp page select --target-index 2 --json",
			"cdp page select --url-contains localhost --json",
		},
		"cdp page reload": {
			"cdp page reload --target <target-id> --json",
			"cdp page reload --target-index 2 --json",
			"cdp page reload --url-contains localhost --ignore-cache --json",
		},
		"cdp page back": {
			"cdp page back --target <target-id> --json",
			"cdp page back --target-index 2 --json",
		},
		"cdp page forward": {
			"cdp page forward --target <target-id> --json",
			"cdp page forward --target-index 2 --json",
		},
		"cdp page activate": {
			"cdp page activate --target <target-id> --json",
			"cdp page activate --target-index 2 --json",
		},
		"cdp page close": {
			"cdp page close --target <target-id> --wait-gone --max-attempts 3 --json",
			"cdp page close --target-index 2 --wait-gone --json",
		},
		"cdp page cleanup": {
			"cdp --browser-mode headed page cleanup --json",
			"cdp --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --force --wait-gone --max-attempts 3 --close-concurrency 4 --max 25 --json",
			"cdp page cleanup --root-task-id research-run --close --force --json",
			"cdp page cleanup --workflow-created --close --include-url localhost --json",
			"cdp page cleanup --target <target-id> --force --json",
			"cdp page cleanup --close --max 10 --exclude-url localhost --json",
		},
		"cdp open": {
			"cdp open https://example.com --json",
			"cdp open https://example.com --run-id run-20260612 --task-id search-01 --root-task-id search --json",
			"cdp open https://example.com --reuse --url-contains example.com --budget-summary --json",
			"cdp open https://example.com --retry transient --max-attempts 3 --json",
			"cdp open https://example.com --new-tab=false --target <target-id> --json",
		},
		"cdp stop-state": {
			"cdp stop-state classify --text 'Sign in to continue' --json",
			"cdp stop-state classify --target <target-id> --json",
		},
		"cdp stop-state classify": {
			"cdp stop-state classify --text 'Sign in to continue' --json",
			"cdp stop-state classify --title 'Access denied' --json",
			"cdp stop-state classify --url https://www.google.com/sorry/index --json",
			"cdp stop-state classify --text 'Oops, something went wrong' --rule-text-contains google_page_error='Something went wrong' --json",
			"cdp stop-state classify --target <target-id> --json",
		},
		"cdp eval": {
			"cdp eval 'document.title' --json",
			"cdp eval 'document.title' --retry transient --max-attempts 3 --json",
			"cdp eval 'Array.from(document.querySelectorAll(\"article\"), el => el.innerText)' --url-contains x.com --json",
			"cdp eval 'document.title' --title-contains Example --json",
			"cdp eval 'document.title' --target-index 2 --json",
		},
		"cdp observe": {
			"cdp observe --json",
			"cdp observe --selector 'button, a[href], input' --limit 30 --json",
			"cdp observe --target-index 2 --json",
		},
		"cdp click": {
			"cdp click 'button.submit' --json",
			"cdp click 'Search' --target-index 2 --json",
			"cdp click 'Search' --by role --role button --json",
			"cdp click 'Search' --by role --role button --trial --json",
			"cdp click 'button[aria-label^=Dictate]' --strategy dom --activate --json",
			"cdp click 'button.covered' --force --json",
			"cdp click '[data-testid=row]' --strategy raw-input --activate --wait-text 'Opened' --timeout 10s --json",
			"cdp click 'Sign in' --by role --role link --wait-popup --wait-popup-url '/oauth' --json",
			"report_target=\"$(cdp --browser-mode headed open 'https://example.com/reports' --task-id report-download --json | jq -r '.page.target_id')\" && cdp --browser-mode headed click 'Download report' --by role --role link --target \"$report_target\" --wait-download --wait-download-filename report --download-dir tmp/downloads --json && cdp --browser-mode headed page close --target \"$report_target\" --wait-gone --json",
			"cdp click 'Delete' --by role --role button --wait-dialog --wait-dialog-action dismiss --json",
			"cdp click 'Upload file' --by label --wait-file-chooser --wait-file-chooser-mode single --json",
			"cdp click 'Save' --by role --role button --wait-request --wait-request-match-url /api --wait-request-method POST --json",
			"cdp click 'Save' --by role --role button --wait-response --wait-response-match-url /api --wait-response-status 200 --json",
			"cdp click 'Search' --by role --role button --wait-url-contains '/results' --json",
			"cdp click 'button[type=submit]' --wait-text 'Results' --json",
			"cdp click 'button.submit' --wait-selector '.toast-success' --diagnostics-out tmp/click.local.json --json",
		},
		"cdp text": {
			"cdp text main --json",
			"cdp text main --retry transient --max-attempts 3 --json",
			"cdp text article --limit 10 --url-contains localhost --json",
			"cdp text article --target-index 2 --json",
		},
		"cdp locator": {
			"cdp locator find Search --by label --json",
			"cdp locator find Submit --by role --role button --json",
		},
		"cdp locator find": {
			"cdp locator find Search --by label --json",
			"cdp locator find Submit --by role --role button --json",
			"cdp locator find cdp_demo --by test-id --test-id-attr data-testid --json",
			"cdp locator find Search --target-index 2 --json",
		},
		"cdp fill": {
			"cdp fill input[name='email'] user@example.com --json",
			"cdp fill input[name='email'] user@example.com --target-index 2 --json",
			"cdp fill 'Search Wikipedia' Aarhus --by label --json",
			"cdp fill 'Search Wikipedia' Aarhus --by label --wait-selector '.suggestions' --json",
			"cdp fill 'Search Wikipedia' Aarhus --by label --wait-url-contains /search --json",
			"cdp fill 'Search Wikipedia' Aarhus --by label --trial --json",
			"cdp fill 'input[type=hidden][name=token]' value --force --json",
			"cdp fill textarea#notes \"first line\\nsecond line\" --url-contains localhost --json",
		},
		"cdp type": {
			"cdp type input[name='email'] user@example.com --json",
			"cdp type input[name='email'] user@example.com --target-index 2 --json",
			"cdp type 'Search Wikipedia' Aarhus --by label --json",
			"cdp type 'Search Wikipedia' Aarhus --by label --wait-text Results --json",
			"cdp type 'Search Wikipedia' Aarhus --by label --wait-url-contains /search --json",
			"cdp type 'Search Wikipedia' Aarhus --by label --trial --json",
			"cdp type 'input[type=hidden][name=token]' value --force --json",
			"cdp type textarea#notes \"typed characters\" --url-contains localhost --json",
		},
		"cdp press": {
			"cdp press Enter --json",
			"cdp press Enter --selector 'input[name=\"q\"]' --target-index 2 --json",
			"cdp press Tab --selector 'input[name=\"q\"]' --json",
			"cdp press Enter 'Search Wikipedia' --by label --json",
			"cdp press Enter 'Search Wikipedia' --by label --wait-text Results --json",
			"cdp press Enter 'Search Wikipedia' --by label --wait-url-contains /search --json",
			"cdp press Enter 'Search Wikipedia' --by label --trial --json",
		},
		"cdp insert-text": {
			"cdp insert-text '[contenteditable=true]' hello --json",
			"cdp insert-text '[contenteditable=true]' hello --target-index 2 --json",
		},
		"cdp hover": {
			"cdp hover button.primary --json",
			"cdp hover button.primary --target-index 2 --json",
			"cdp hover 'Save changes' --by role --role button --trial --json",
			"cdp hover '#covered-action' --force --url-contains localhost --json",
		},
		"cdp drag": {
			"cdp drag '.draggable' 10 20 --json",
			"cdp drag '.draggable' 10 20 --target-index 2 --json",
			"cdp drag drag-target 8 12 --by test-id --trial --json",
			"cdp drag '#drag-handle' -8 12 --force --url-contains localhost --json",
		},
		"cdp frames": {
			"cdp frames --json",
			"cdp frames --url-contains localhost --json",
			"cdp frames --target-index 2 --json",
		},
		"cdp html": {
			"cdp html main --max-chars 4000 --json",
			"cdp html '#root' --limit 1 --json",
			"cdp html body --diagnose-empty --json",
			"cdp html body --target-index 2 --json",
		},
		"cdp dom query": {
			"cdp dom query button --json",
			"cdp dom query '[role=\"button\"]' --limit 20 --json",
			"cdp dom query button --target-index 2 --json",
		},
		"cdp css inspect": {
			"cdp css inspect main --json",
			"cdp css inspect '.panel' --url-contains localhost --json",
			"cdp css inspect main --target-index 2 --json",
		},
		"cdp layout overflow": {
			"cdp layout overflow --json",
			"cdp layout overflow --selector 'body *' --limit 20 --json",
			"cdp layout overflow --target-index 2 --json",
		},
		"cdp wait text": {
			"cdp wait text Ready --timeout 10s --json",
			"cdp wait text 'Dashboard loaded' --url-contains localhost --json",
			"cdp wait text Ready --target-index 2 --json",
		},
		"cdp wait selector": {
			"cdp wait selector main --timeout 10s --json",
			"cdp wait selector '[data-ready=\"true\"]' --poll 500ms --json",
			"cdp wait selector main --target-index 2 --json",
		},
		"cdp wait url": {
			"cdp wait url /results --mode contains --timeout 10s --json",
			"cdp wait url https://example.com/checkout --mode exact --poll 100ms --json",
			"cdp wait url /results --mode contains --target-index 2 --json",
		},
		"cdp wait locator": {
			"cdp wait locator 'Search' --by role --role button --strict --timeout 10s --json",
			"cdp wait locator 'Dashboard loaded' --by text --timeout 10s --json",
			"cdp wait locator 'Search' --by text --target-index 2 --json",
		},
		"cdp wait eval": {
			"cdp wait eval 'window.__rendered === true' --timeout 10s --json",
			"cdp wait eval 'document.readyState === \"complete\"' --poll 500ms --json",
			"cdp wait eval 'window.__rendered === true' --retry transient --max-attempts 3 --json",
			"cdp wait eval 'window.__stageState()' --ready-expr 'value.terminalCondition === \"fare_rows\"' --out-dir tmp/stage-ready --json",
			"cdp wait eval 'window.__stageState()' --ready-field ready --classify-stop-state --rule-text-contains google_page_error='Something went wrong' --json",
			"cdp wait eval 'window.__rendered === true' --target-index 2 --json",
		},
		"cdp wait load-state": {
			"cdp wait load-state load --timeout 10s --json",
			"cdp wait load-state domcontentloaded --poll 100ms --json",
			"cdp wait load-state load --target-index 2 --json",
		},
		"cdp wait request": {
			"cdp wait request --match-url /api/search --method POST --timeout 10s --json",
			"cdp wait request --url https://example.com/api --resource-type Fetch --json",
			"cdp wait request --target-index 2 --match-url /api/search --method POST --json",
		},
		"cdp wait response": {
			"cdp wait response --match-url /api/search --status 200 --timeout 10s --json",
			"cdp wait response --method GET --status-min 200 --status-max 399 --json",
			"cdp wait response --target-index 2 --match-url /api/search --status 200 --json",
		},
		"cdp wait network-idle": {
			"cdp wait network-idle --idle 500ms --timeout 10s --json",
			"cdp wait network-idle --ignore-url-contains /events --idle 1s --json",
			"cdp wait network-idle --target-index 2 --idle 500ms --json",
		},
		"cdp wait dialog": {
			"cdp wait dialog --type confirm --action dismiss --timeout 10s --json",
			"cdp wait dialog --message-contains 'Delete item?' --action accept --json",
			"cdp wait dialog --target-index 2 --type confirm --action dismiss --json",
		},
		"cdp wait file-chooser": {
			"cdp wait file-chooser --mode single --timeout 10s --json",
			"cdp wait file-chooser --mode multiple --json",
			"cdp wait file-chooser --target-index 2 --mode single --json",
		},
		"cdp wait popup": {
			"cdp wait popup --match-url /oauth/callback --timeout 10s --json",
			"cdp wait popup --title-contains Checkout --match-title Receipt --json",
			"cdp wait popup --target-index 2 --match-url /oauth/callback --json",
		},
		"cdp wait download": {
			"cdp wait download --match-url /report.csv --filename-contains report --download-dir tmp/downloads --json",
			"cdp wait download --state started --timeout 10s --json",
			"cdp wait download --target-index 2 --match-url /report.csv --download-dir tmp/downloads --json",
		},
		"cdp snapshot": {
			"cdp snapshot --selector body --json",
			"cdp snapshot --selector article --limit 10 --url-contains x.com --json",
			"cdp snapshot --selector body --diagnose-empty --json",
			"cdp snapshot --selector body --target-index 2 --json",
		},
		"cdp screenshot": {
			"cdp screenshot --out tmp/page.png --json",
			"cdp screenshot --preset mobile --out tmp/mobile.png --json",
			"cdp screenshot --target <target-id> --full-page --out tmp/page.png --json",
			"cdp screenshot --target-index 2 --out tmp/page.png --json",
			"cdp screenshot --tile-full-page --out-dir tmp/page-tiles --json",
			"cdp screenshot --url-contains localhost --out tmp/page.png --json",
			"cdp screenshot --navigate 'https://example.com' --wait 2s --out tmp/page.png --json",
			"cdp screenshot --element '.mermaid svg' --out tmp/diagram.png --json",
			"cdp screenshot --out tmp/page.png --crop --crop-padding 10 --json",
		},
		"cdp screenshot render": {
			"cdp screenshot render ./diagram.html --out tmp/diagram.png --width 1800 --height 1100 --dpr 2 --wait-for 'window.__rendered' --json",
			"cdp screenshot render ./diagram.html --out tmp/diagram.png --serve --wait 3s --crop --json",
		},
		"cdp console": {
			"cdp console --json",
			"cdp console --errors --wait 2s --json",
			"cdp console --target-index 2 --wait 2s --json",
			"cdp console --url-contains localhost --types error,warning --json",
			"cdp console --target <target-id> --wait 30s --ready-file tmp/console.ready.json --json",
		},
		"cdp network": {
			"cdp network --wait 2s --json",
			"cdp network --failed --url-contains localhost --json",
			"cdp network --target-index 2 --wait 2s --json",
			"cdp network --target <target-id> --wait 30s --ready-file tmp/network.ready.json --json",
		},
		"cdp network capture": {
			"cdp network capture --reload --wait 20s --out tmp/network.local.json --json",
			"cdp network capture --redact safe --har-out tmp/network.har --json",
			"cdp network capture --target-index 2 --redact safe --wait 2s --json",
			"cdp network capture --redact safe --body-out-dir tmp/network-bodies --body-artifact-limit 20 --json",
			"cdp network capture --include-websockets --include-websocket-payloads --out tmp/network-with-ws.local.json --json",
			"cdp network capture --url-contains localhost --redact safe --out tmp/network-shareable.json --json",
		},
		"cdp network websocket": {
			"cdp network websocket --wait 20s --include-payloads --out tmp/ws.local.json --json",
			"cdp network websocket --target-index 2 --redact safe --wait 2s --json",
			"cdp network websocket --url-contains localhost --redact safe --json",
		},
		"cdp network block": {
			"cdp network block --pattern '*://*/analytics/*' --duration 10s --url-contains localhost --json",
		},
		"cdp network mock": {
			`cdp network mock --rule '{"url_pattern":"*://*/api/config","method":"GET","status":200,"body":"{\"enabled\":true}","max_matches":1}' --duration 10s --json`,
		},
		"cdp storage": {
			"cdp storage list --url-contains localhost --json",
			"cdp storage snapshot --out tmp/storage.local.json --json",
		},
		"cdp storage list": {
			"cdp storage list --url-contains localhost --json",
			"cdp storage list --include localStorage,sessionStorage,cookies,cache --json",
		},
		"cdp storage get": {
			"cdp storage get localStorage feature_flag --url-contains localhost --json",
		},
		"cdp storage set": {
			"cdp storage set localStorage feature_flag enabled --url-contains localhost --json",
			"cdp storage set sessionStorage seed @tmp/seed.json --json",
		},
		"cdp storage delete": {
			"cdp storage delete localStorage feature_flag --url-contains localhost --json",
		},
		"cdp storage clear": {
			"cdp storage clear sessionStorage --url-contains localhost --json",
		},
		"cdp storage snapshot": {
			"cdp storage snapshot --out tmp/app-storage.local.json --json",
			"cdp storage snapshot --redact safe --out tmp/app-storage-shareable.json --json",
		},
		"cdp storage diff": {
			"cdp storage diff --left tmp/before.local.json --right tmp/after.local.json --json",
		},
		"cdp storage cookies": {
			"cdp storage cookies list --url 'https://example.com' --json",
		},
		"cdp storage cookies list": {
			"cdp storage cookies list --url-contains localhost --json",
		},
		"cdp storage cookies set": {
			"cdp storage cookies set --url 'https://example.com' --name feature_flag --value enabled --json",
		},
		"cdp storage cookies delete": {
			"cdp storage cookies delete --url 'https://example.com' --name feature_flag --json",
		},
		"cdp workflow youtube cookies": {
			"cdp --browser-mode headed workflow youtube cookies --out ~/.local/state/yt-dlp/cookies.txt --json",
			"cdp --browser-mode headed workflow youtube cookies --settle 5s --json",
		},
		"cdp storage indexeddb": {
			"cdp storage indexeddb list --url-contains localhost --json",
			"cdp storage indexeddb dump app records --limit 100 --json",
		},
		"cdp storage indexeddb list": {
			"cdp storage indexeddb list --url-contains localhost --json",
		},
		"cdp storage indexeddb get": {
			"cdp storage indexeddb get app settings feature --json",
			"cdp storage indexeddb get app records '[\"compound\",1]' --key-json --json",
		},
		"cdp storage indexeddb put": {
			"cdp storage indexeddb put app settings feature '{\"enabled\":true}' --json",
			"cdp storage indexeddb put app settings feature @tmp/value.json --json",
		},
		"cdp storage indexeddb delete": {
			"cdp storage indexeddb delete app settings feature --json",
		},
		"cdp storage indexeddb clear": {
			"cdp storage indexeddb clear app settings --json",
		},
		"cdp storage cache": {
			"cdp storage cache list --url-contains localhost --json",
		},
		"cdp storage cache list": {
			"cdp storage cache list --cache app-cache --json",
			"cdp storage cache list --request-url-contains /api --json",
		},
		"cdp storage cache get": {
			"cdp storage cache get app-cache https://example.com/api/me --max-body-bytes 4096 --json",
		},
		"cdp storage cache put": {
			"cdp storage cache put app-cache https://example.com/api/fixture '{\"ok\":true}' --content-type application/json --json",
			"cdp storage cache put app-cache https://example.com/api/fixture @tmp/fixture.json --json",
		},
		"cdp storage cache delete": {
			"cdp storage cache delete app-cache https://example.com/api/fixture --json",
		},
		"cdp storage cache clear": {
			"cdp storage cache clear app-cache --json",
			"cdp storage cache clear --all --json",
		},
		"cdp storage service-workers": {
			"cdp storage service-workers list --url-contains localhost --json",
		},
		"cdp storage service-workers list": {
			"cdp storage service-workers list --url-contains localhost --json",
		},
		"cdp storage service-workers unregister": {
			"cdp storage service-workers unregister --scope https://example.com/ --json",
			"cdp storage service-workers unregister --all --json",
		},
		"cdp protocol metadata": {
			"cdp protocol metadata --json",
		},
		"cdp protocol domains": {
			"cdp protocol domains --json",
			"cdp protocol domains --experimental --json",
		},
		"cdp protocol search": {
			"cdp protocol search screenshot --json",
			"cdp protocol search console --kind event --json",
		},
		"cdp protocol describe": {
			"cdp protocol describe Page.captureScreenshot --json",
		},
		"cdp protocol examples": {
			"cdp protocol examples Page.captureScreenshot --json",
			"cdp protocol examples Runtime.evaluate --json",
		},
		"cdp protocol exec": {
			"cdp protocol exec Browser.getVersion --params '{}' --json",
			"cdp protocol exec Runtime.evaluate --target <target-id> --params '{\"expression\":\"document.title\",\"returnByValue\":true}' --json",
			"cdp protocol exec Runtime.evaluate --target-index 2 --params '{\"expression\":\"document.title\",\"returnByValue\":true}' --json",
			"cdp protocol exec Runtime.evaluate --target-type service_worker --url-contains chrome-extension:// --params '{\"expression\":\"Object.keys(globalThis).slice(0,50)\",\"returnByValue\":true}' --json",
			"cdp protocol exec Page.captureScreenshot --target <target-id> --params '{\"format\":\"png\"}' --save tmp/page.png --json",
			"cdp protocol exec DOM.getDocument --url-contains localhost --json",
		},
		"cdp workflow verify": {
			"cdp workflow verify 'https://example.com' --json",
		},
		"cdp workflow debug-bundle": {
			"cdp workflow debug-bundle --url 'https://example.com' --since 5s --screenshot-view --out-dir tmp/debug-bundle --task-id research-preflight --json",
			"cdp workflow debug-bundle --target <target-id> --out-dir tmp/debug-bundle --run-id run-1 --task-id task-1 --stage selection --json",
			"cdp workflow debug-bundle --target <target-id> --out-dir tmp/debug-bundle --inline-payloads --redact safe --json",
			"cdp workflow debug-bundle --target <target-id> --reload=false --ignore-cache=false --json",
		},
		"cdp workflow action-capture": {
			"cdp workflow action-capture --action click:'button.submit' --include network,console,dom,text,a11y,screenshot --wait-after 2s --evidence-out-dir tmp/action-capture --json",
			"cdp workflow action-capture --action click:'button.submit' --include network --include-bodies json,text --body-url-contains /api/ --body-limit 262144 --json",
			"cdp workflow action-capture --action click:'button.submit' --include screenshot --screenshot-full-page --evidence-out-dir tmp/action-capture --json",
			"cdp workflow action-capture --action press:Enter --selector 'input[name=q]' --include network,console,dom,text --out tmp/action-capture.json --json",
		},
		"cdp workflow submit-search": {
			"cdp workflow submit-search 'Search Wikipedia' Aarhus --by label --wait-url-contains /search --json",
			"cdp workflow submit-search 'From' Aarhus --by label --suggestion 'Aarhus Denmark' --suggestion-by text --submit none --wait-selector '.destination-selected' --json",
			"cdp workflow submit-search 'Search' 'agentic engineering' --by label --input-mode type --wait-text Results --json",
			"cdp workflow submit-search 'Search' 'agentic engineering' --by label --suggestion 'Save' --suggestion-by role --suggestion-role button --submit none --wait-response-match-url /api/search --wait-response-status 200 --json",
			"cdp workflow submit-search 'Search' 'agentic engineering' --by label --submit none --wait-load-state domcontentloaded --json",
			"cdp workflow submit-search 'Search' 'agentic engineering' --by label --submit none --wait-selector '.suggestions' --json",
		},
		"cdp workflow a11y": {
			"cdp workflow a11y 'https://example.com' --wait 5s --json",
			"cdp workflow a11y 'https://example.com' --limit 50 --wait 5s --json",
		},
		"cdp workflow visible-posts": {
			"cdp workflow visible-posts 'https://x.com/<handle>' --limit 5 --json",
			"cdp workflow visible-posts 'https://example.com/feed' --selector article --wait 30s --json",
		},
		"cdp workflow hacker-news": {
			"cdp workflow hacker-news --limit 10 --json",
			"cdp workflow hacker-news 'https://news.ycombinator.com/news' --wait 30s --json",
		},
		"cdp workflow hacker-news collect": {"cdp --browser-mode headed workflow hacker-news collect 'https://news.ycombinator.com/item?id=46641042' --limit 500 --json"},
		"cdp workflow reddit posts": {
			"cdp --browser-mode headed workflow reddit posts 'https://www.reddit.com/r/formula1/top/?t=week' --limit 200 --json",
			"cdp workflow reddit posts 'https://www.reddit.com/r/golang/new/' --limit 100 --json",
		},
		"cdp workflow reddit collect": {
			"cdp --browser-mode headed workflow reddit collect 'https://www.reddit.com/r/formula1/top/?t=week' --limit 200 --json",
			"cdp --browser-mode headed workflow reddit collect 'https://www.reddit.com/r/codex/comments/1v010h6/the_sun_came_out/' --limit 500 --json",
			"cdp --browser-mode headed workflow reddit collect 'https://www.reddit.com/user/celticpaladin/comments/' --limit 200 --json",
		},
		"cdp workflow x collect": {
			"cdp --browser-mode headed workflow x collect 'https://x.com/karpathy/status/2079610838143623371' --limit 500 --json",
			"cdp --browser-mode headed workflow x collect 'https://x.com/karpathy' --limit 200 --json",
		},
		"cdp workflow linkedin collect": {
			"cdp --browser-mode headed workflow linkedin collect 'https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD/' --limit 500 --json",
			"cdp --browser-mode headed workflow linkedin collect 'https://www.linkedin.com/company/the-pragmatic-engineer/posts/' --limit 200 --json",
		},
		"cdp workflow arxiv collect": {"cdp --browser-mode headed workflow arxiv collect 'https://arxiv.org/abs/2604.12374' --json"},
		"cdp workflow pdf-to-markdown": {
			"cdp workflow pdf-to-markdown tmp/downloads/paper.pdf --out-dir tmp/paper-markdown --json",
			"pdf_target=\"$(cdp --browser-mode headed open 'https://example.com/paper' --task-id pdf-download --json | jq -r '.page.target_id')\" && cdp --browser-mode headed click 'Download PDF' --by role --role link --target \"$pdf_target\" --wait-download --download-dir tmp/downloads --json && cdp --browser-mode headed page close --target \"$pdf_target\" --wait-gone --json && cdp workflow pdf-to-markdown tmp/downloads/paper.pdf --json",
		},
		"cdp workflow google-translate": {
			"cdp --browser-mode headed workflow google-translate --text 'Dette er en kort test.' --source da --target en --json",
			"cdp --browser-mode headed workflow google-translate --file \"$HOME/Downloads/Pelvic floor training confirmation.pdf\" --target en --out-dir tmp/translated-scan --wait 5m --json",
			"cdp --browser-mode headed workflow google-translate --url 'https://da.wikipedia.org/wiki/Danmark' --target en --output tmp/denmark.txt --json",
			"cdp --browser-mode headed workflow google-translate --text-file tmp/danish.txt --source da --target en --chunk-size 4800 --wait 5m --json",
		},
		"cdp workflow google-maps-directions": {
			"cdp --browser-mode headed workflow google-maps-directions 'Kongens Lyngby, Denmark' 'Stege, Denmark' --json",
			"cdp --browser-mode headed workflow google-maps-directions 'Stege, Denmark' 'Møn Is, Hovgårdsvej 4, 4780 Stege, Denmark' --wait 30s --json",
		},
		"cdp workflow perf": {
			"cdp workflow perf 'https://example.com' --wait 5s --json",
			"cdp workflow perf 'https://example.com' --wait 5s --trace tmp/perf.local.json --trace-max-bytes 16777216 --redact safe --json",
		},
		"cdp workflow console-errors": {
			"cdp workflow console-errors --wait 2s --json",
			"cdp workflow console-errors --url-contains localhost --json",
		},
		"cdp workflow network-failures": {
			"cdp workflow network-failures --wait 2s --json",
			"cdp workflow network-failures --url-contains localhost --json",
		},
		"cdp workflow page-load": {
			"cdp workflow page-load 'https://example.com' --wait 10s --json",
			"cdp workflow page-load --url-contains localhost --reload --include console,network,performance --out tmp/page-load.local.json --json",
			"cdp workflow page-load --target <target-id> --wait 30s --ready-file tmp/page-load.ready.json --json",
		},
		"cdp workflow rendered-extract": {
			"cdp workflow rendered-extract 'https://example.com' --wait 20s --settle 2s --out-dir tmp/rendered-example --json",
			"cdp --browser-mode headed workflow rendered-extract 'https://arxiv.org/pdf/2603.26487' --content-extractor auto --out-dir tmp/rendered-arxiv --json",
			"cdp --browser-mode headed workflow rendered-extract 'https://news.ycombinator.com/item?id=46641042' --content-extractor auto --out-dir tmp/rendered-hn --json",
			"cdp --browser-mode headed workflow rendered-extract 'https://x.com/karpathy/status/2079610838143623371' --content-extractor auto --out-dir tmp/rendered-x --json",
			"cdp --browser-mode headed workflow rendered-extract 'https://x.com/karpathy' --content-extractor auto --out-dir tmp/rendered-x-profile --json",
			"cdp --browser-mode headed workflow rendered-extract 'https://www.linkedin.com/posts/example-activity-7482842673645584386-9aSD' --content-extractor auto --out-dir tmp/rendered-linkedin --json",
			"cdp --browser-mode headed workflow rendered-extract 'https://www.linkedin.com/company/example/posts/' --content-extractor auto --out-dir tmp/rendered-linkedin-company --json",
			"cdp --browser-mode headed workflow rendered-extract 'https://www.reddit.com/r/example/comments/1abc234/a_post/' --content-extractor auto --out-dir tmp/rendered-reddit --json",
			"cdp --browser-mode headed workflow rendered-extract 'https://www.reddit.com/user/example/' --content-extractor auto --out-dir tmp/rendered-reddit-user --json",
			"cdp workflow rendered-extract --target <target-id> --reload --out-dir tmp/rendered-existing --json",
			"cdp workflow rendered-extract --url-contains localhost --out-dir tmp/rendered-selected --json",
			"cdp workflow rendered-extract 'https://www.google.com/search?q=agentic+engineering&safe=active&tbs=qdr:m' --serp google --out-dir tmp/rendered-google --json",
		},
		"cdp workflow web-research": {
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --out-dir tmp/research --json",
			"cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --out-dir tmp/research/pages --json",
		},
		"cdp workflow web-research serp": {
			"printf '%s\\t%s\\n' 'agentic engineering' 'cdr:1,cd_min:07/01/2026,cd_max:07/01/2026' > tmp/research/queries-exact-date.txt && cdp --browser-mode headed workflow web-research serp --query-file tmp/research/queries-exact-date.txt --serp google --out-dir tmp/research/exact-date --json",
			"cdp --browser-mode headed workflow web-research serp --query-file tmp/research/queries.txt --serp google --google-ai auto --wait 30s --settle 3s --out-dir tmp/research/google-ai-overview --json",
			"cdp --browser-mode headed workflow web-research serp --query-file tmp/research/queries.txt --serp google --google-ai mode --wait 30s --settle 3s --out-dir tmp/research/google-ai-mode --json",
			"cdp --browser-mode headed workflow web-research serp --query-file tmp/research/queries.txt --serp google --fallback-serp none --parallel 1 --navigation-delay 30s --result-pages 1 --fast-fail-blocked --blocked-failure-threshold 1 --progress stderr --out-dir tmp/research/progressive-pass --json",
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --serp google --result-pages 3 --max-candidates 200 --wait 30s --settle 3s --candidate-out tmp/research/candidates.json --out-dir tmp/research --json",
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --serp all --parallel-engines --result-pages 2 --out-dir tmp/research-all --json",
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --serp duckduckgo --fallback-serp google --result-pages 2 --out-dir tmp/research-ddg --json",
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --serp bing --result-pages 3 --fast-fail-blocked --blocked-failure-threshold 3 --progress stderr --json",
		},
		"cdp workflow web-research extract": {
			"cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --max-pages 100 --parallel 4 --wait 20s --settle 2s --out-dir tmp/research/pages --json",
			"cdp --browser-mode headed workflow web-research extract --url-file tmp/research/arxiv-hn-urls.txt --content-extractor auto --out-dir tmp/research/source-aware-pages --json",
			"cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --parallel 10 --allow-over-budget --json # supervised high-stress cap",
		},
		"cdp workflow agent": {
			"cdp workflow agent providers --json",
			"cdp workflow agent auth refresh --json",
			"cdp workflow agent capabilities refresh --json",
			"cdp workflow agent chatgpt transcribe --file /path/to/whisper.webm --duration-ms 4200 --json",
			"cdp workflow agent claude capabilities --json",
			"cdp workflow agent claude doctor --json",
			"cdp workflow agent claude auth refresh --json",
			"printf '%s' 'Review this design.' | cdp workflow agent claude ask --stdin --json",
			"cdp workflow agent claude conversations list --limit 30 --json",
			"cdp workflow agent gemini capabilities --json",
			"cdp workflow agent gemini capabilities refresh --json",
			"printf '%s' 'Review this design.' | cdp workflow agent gemini ask --stdin --json",
			"cdp --config cdp.json workflow web-research serp --query-file tmp/queries.txt --serp google --json # agents.google.exclusive_ai_mode selects inline or exclusive AI Mode",
			"cdp workflow web-research serp --query-file tmp/queries.txt --serp google --google-ai auto --json # one-run corporate/Zscaler override",
			"cdp schema webagent-operation --json",
		},
		"cdp workflow agent providers": {
			"cdp workflow agent providers --json",
			"cdp workflow agent providers --jq '.data.providers[] | {provider, implementation_status}'",
		},
	}
	examples["cdp focus"] = []string{"cdp focus input[name=email] --json", "cdp focus input[name=email] --target-index 2 --json"}
	examples["cdp clear"] = []string{"cdp clear input[name=email] --json", "cdp clear input[name=email] --target-index 2 --json"}
	examples["cdp select"] = []string{"cdp select select[name=plan] pro --json", "cdp select select[name=plan] pro --target-index 2 --json", "cdp select Plan pro --by label --wait-text 'Plan selected' --json", "cdp select Plan pro --by label --trial --json", "cdp select '#hidden-plan' pro --force --json"}
	examples["cdp check"] = []string{"cdp check 'Subscribe to newsletter' --by label --json", "cdp check input#subscribe --target-index 2 --json", "cdp check Subscribe --by role --role checkbox --trial --json", "cdp check '#covered-checkbox' --force --json"}
	examples["cdp uncheck"] = []string{"cdp uncheck 'Subscribe to newsletter' --by label --json", "cdp uncheck input#subscribe --target-index 2 --json", "cdp uncheck Subscribe --by role --role checkbox --trial --json", "cdp uncheck '#covered-checkbox' --force --json"}
	examples["cdp file"] = []string{"cdp file input[type=file] tmp/upload.txt --json", "cdp file 'Upload file' tmp/upload.txt --by label --target-index 2 --trial --json"}
	examples["cdp file chooser"] = []string{"cdp file chooser 247 tmp/upload.txt --target <target-id> --trial --json", "cdp file chooser 247 tmp/first.epub tmp/second.epub --target-index 2 --json"}
	examples["cdp scroll"] = []string{"cdp scroll '#results' --json", "cdp scroll '#results' --target-index 2 --json", "cdp scroll 'Load more' --by role --role button --trial --json", "cdp scroll 'footer' --block end --inline nearest --json"}
	examples["cdp dialog"] = []string{"cdp dialog accept --prompt-text yes --json", "cdp dialog dismiss --json"}
	examples["cdp dialog accept"] = []string{"cdp dialog accept --prompt-text yes --json"}
	examples["cdp dialog dismiss"] = []string{"cdp dialog dismiss --json"}
	examples["cdp emulate"] = []string{"cdp emulate viewport --preset mobile --json", "cdp emulate user-agent --user-agent 'Mozilla/5.0 ...' --json", "cdp emulate timezone --timezone-id UTC --json", "cdp emulate locale --locale de-DE --json", "cdp emulate color-scheme --scheme dark --json", "cdp emulate network --preset slow-3g --json"}
	examples["cdp emulate viewport"] = []string{"cdp emulate viewport --width 390 --height 844 --mobile --dpr 1 --json", "cdp emulate viewport --preset iphone-12 --json"}
	examples["cdp emulate clear"] = []string{"cdp emulate clear --json"}
	examples["cdp emulate media"] = []string{"cdp emulate media --prefers-color-scheme dark --json"}
	examples["cdp emulate color-scheme"] = []string{"cdp emulate color-scheme --scheme dark --json", "cdp emulate color-scheme --scheme light --json"}
	examples["cdp emulate user-agent"] = []string{"cdp emulate user-agent --user-agent 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/125 Safari/537.36' --platform Linux --json"}
	examples["cdp emulate geolocation"] = []string{"cdp emulate geolocation --latitude 55.6761 --longitude 12.5683 --accuracy 100 --json"}
	examples["cdp emulate timezone"] = []string{"cdp emulate timezone --timezone-id UTC --json", "cdp emulate timezone --timezone-id America/New_York --json"}
	examples["cdp emulate locale"] = []string{"cdp emulate locale --locale de-DE --json", "cdp emulate locale --locale en-US --json"}
	examples["cdp emulate cpu"] = []string{"cdp emulate cpu --rate 4 --json", "cdp emulate cpu --rate 1 --json"}
	examples["cdp emulate network"] = []string{"cdp emulate network --preset slow-3g --json", "cdp emulate network --latency 100 --download-kbps 750 --upload-kbps 250 --json", "cdp emulate network --preset none --json"}
	examples["cdp permissions"] = []string{"cdp permissions grant notifications --origin https://example.com --json", "cdp permissions set notifications --setting denied --origin https://example.com --json", "cdp permissions reset --json"}
	examples["cdp permissions grant"] = []string{"cdp permissions grant notifications --origin https://example.com --json", "cdp permissions grant geolocation notifications --origin https://example.com --json"}
	examples["cdp permissions set"] = []string{"cdp permissions set notifications --setting denied --origin https://example.com --json", "cdp permissions set geolocation --setting prompt --origin https://example.com --json"}
	examples["cdp permissions reset"] = []string{"cdp permissions reset --json"}
	examples["cdp a11y"] = []string{"cdp a11y tree --depth 4 --json"}
	examples["cdp a11y tree"] = []string{"cdp a11y tree --target <target-id> --depth 4 --json", "cdp a11y tree --target-index 2 --json"}
	examples["cdp a11y find"] = []string{"cdp a11y find --role button --name Save --json", "cdp a11y find --role button --target-index 2 --json"}
	examples["cdp a11y node"] = []string{"cdp a11y node button[type=submit] --json", "cdp a11y node button --target-index 2 --json"}
	examples["cdp a11y snapshot"] = []string{"cdp a11y snapshot --selector body --depth 4 --json", "cdp a11y snapshot --limit 20 --json", "cdp a11y snapshot --target-index 2 --json"}
	examples["cdp assert aria-snapshot"] = []string{"cdp assert aria-snapshot --expected '- button \"Save\"' --selector body --target-index 2 --json", "cdp assert aria-snapshot --file tmp/aria-snapshot.txt --mode exact --json"}
	examples["cdp perf summary"] = []string{"cdp perf summary --duration 5s --json", "cdp perf summary --target-index 2 --duration 5s --json"}
	examples["cdp memory counters"] = []string{"cdp memory counters --json", "cdp memory counters --target-index 2 --json"}
	examples["cdp memory heap-snapshot"] = []string{"cdp memory heap-snapshot --out tmp/page.heapsnapshot --json", "cdp memory heap-snapshot --target-index 2 --out tmp/page.heapsnapshot --json"}
	examples["cdp events"] = []string{"cdp events tap --duration 10s --json", "cdp events stream --target <target-id> --enable page,runtime,DOM --match Page.loadEventFired,DOM.documentUpdated --duration 30s --json"}
	examples["cdp events tap"] = []string{
		"cdp events tap --enable page,network,runtime --match Page.lifecycleEvent,Network.loadingFailed --duration 10s --json",
		"cdp events tap --target-index 2 --enable page,network --match Page.loadEventFired,Network.loadingFailed --duration 10s --max-events 50 --json",
		"cdp events tap --target <target-id> --enable DOM,Performance --match DOM.documentUpdated --duration 10s --json",
		"cdp events tap --target <target-id> --duration 30s --ready-file tmp/events.ready.json --json",
	}
	examples["cdp events stream"] = []string{
		"cdp events stream --target <target-id> --enable page,runtime,DOM --match Page.loadEventFired,DOM.documentUpdated --duration 30s --json",
		"printf '+Runtime.consoleAPICalled\\n' | cdp events stream --target-index 1 --json",
		"printf '+DOM.documentUpdated\\n' | cdp events stream --target-index 1 --enable page --json",
	}
	examples["cdp events wait"] = []string{
		"cdp events wait --file tmp/events.jsonl --method Page.loadEventFired --timeout 20s --json",
		"cdp events wait --file tmp/events.jsonl --from-offset 123 --contains /results/ --print-offset --json",
	}
	examples["cdp events interactions"] = []string{
		"cdp events interactions --target-index 1 --match click,scroll --duration 30s --max-events 50 --json",
		"cdp events interactions --target <target-id> --match click --max-events 1 --ready-file tmp/interaction.ready.json --json",
	}
	examples["cdp protocol compat"] = []string{"cdp protocol compat --requires Target.attachToTarget,Runtime.evaluate --json", "cdp protocol compat --workflow debug-bundle --json"}
	examples["cdp workflow feeds"] = []string{"cdp workflow feeds 'https://example.com' --wait-load 10s --json", "cdp workflow feeds 'https://example.com' --keep-open --json"}
	examples["cdp workflow responsive-audit"] = []string{"cdp workflow responsive-audit 'https://example.com' --viewports desktop,tablet,mobile --include console,network,layout,screenshot,a11y --json"}
	examples["cdp workflow lighthouse"] = []string{"cdp --browser-mode headless workflow lighthouse 'https://example.com' --categories accessibility,best-practices --out-dir tmp/lighthouse --redact safe --json"}
	examples["cdp form values"] = []string{"cdp form values --url-contains localhost --json"}
	examples["cdp form get"] = []string{"cdp form get 'textarea[aria-label=Output]' --json"}
	examples["cdp assert value"] = []string{"cdp assert value 'textarea[aria-label=Output]' expected --mode exact --target-index 2 --timeout 5s --json", "cdp assert value 'Search' expected --by label --poll 100ms --json"}
	examples["cdp assert text"] = []string{"cdp assert text 'Saved successfully' --mode contains --target-index 2 --timeout 5s --json", "cdp assert text 'Search' 'Search' --by role --role button --poll 100ms --json"}
	examples["cdp assert url"] = []string{"cdp assert url example.com --mode contains --target-index 2 --timeout 5s --json", "cdp assert url '^https://example\\.com/' --mode regex --poll 100ms --json"}
	examples["cdp assert title"] = []string{"cdp assert title 'Example Domain' --mode exact --target-index 2 --timeout 5s --json", "cdp assert title Checkout --mode contains --poll 100ms --json"}
	examples["cdp assert count"] = []string{"cdp assert count '.result-item' 10 --target-index 2 --timeout 5s --json", "cdp assert count 'Search result' 3 --by role --role listitem --poll 100ms --json"}
	examples["cdp assert attribute"] = []string{"cdp assert attribute 'button[type=submit]' data-state ready --mode exact --target-index 2 --timeout 5s --json", "cdp assert attribute Checkout aria-expanded true --by role --role button --poll 100ms --json"}
	examples["cdp assert class"] = []string{"cdp assert class 'button[type=submit]' primary --target-index 2 --timeout 5s --json", "cdp assert class Checkout primary --by role --role button --poll 100ms --json"}
	examples["cdp assert focused"] = []string{"cdp assert focused 'input[name=q]' --target-index 2 --timeout 5s --json", "cdp assert focused 'Search' --by label --poll 100ms --json"}
	examples["cdp assert css"] = []string{"cdp assert css 'button[type=submit]' background-color 'rgb(20, 92, 160)' --mode exact --target-index 2 --timeout 5s --json", "cdp assert css Checkout color 'rgb(255, 255, 255)' --by role --role button --poll 100ms --json"}
	examples["cdp assert role"] = []string{"cdp assert role 'button[type=submit]' button --target-index 2 --timeout 5s --json", "cdp assert role Checkout button --by role --role button --poll 100ms --json"}
	examples["cdp assert name"] = []string{"cdp assert name 'button[type=submit]' Submit --mode exact --target-index 2 --timeout 5s --json", "cdp assert name Checkout Checkout --by role --role button --poll 100ms --json"}
	examples["cdp assert attached"] = []string{"cdp assert attached '#app' --target-index 2 --timeout 5s --json", "cdp assert attached 'Search' --by role --role button --poll 100ms --json"}
	examples["cdp assert detached"] = []string{"cdp assert detached '#loading-spinner' --target-index 2 --timeout 5s --json", "cdp assert detached 'Gone' --by text --poll 100ms --json"}
	examples["cdp assert visible"] = []string{"cdp assert visible 'button[type=submit]' --target-index 2 --timeout 5s --json", "cdp assert visible 'Search' --by role --role button --poll 100ms --json"}
	examples["cdp assert hidden"] = []string{"cdp assert hidden '#loading-spinner' --target-index 2 --timeout 5s --json", "cdp assert hidden 'Dismiss' --by role --role button --poll 100ms --json"}
	examples["cdp assert in-viewport"] = []string{"cdp assert in-viewport '#footer' --target-index 2 --timeout 5s --json", "cdp assert in-viewport 'Load more' --by role --role button --poll 100ms --json"}
	examples["cdp assert enabled"] = []string{"cdp assert enabled 'button[type=submit]' --target-index 2 --timeout 5s --json", "cdp assert enabled 'Search' --by role --role button --poll 100ms --json"}
	examples["cdp assert disabled"] = []string{"cdp assert disabled 'button[disabled]' --target-index 2 --timeout 5s --json", "cdp assert disabled 'Submit' --by role --role button --poll 100ms --json"}
	examples["cdp assert editable"] = []string{"cdp assert editable 'input[name=email]' --target-index 2 --timeout 5s --json", "cdp assert editable 'Search' --by label --poll 100ms --json"}
	examples["cdp assert readonly"] = []string{"cdp assert readonly 'textarea[readonly]' --target-index 2 --timeout 5s --json", "cdp assert readonly 'Search' --by label --poll 100ms --json"}
	examples["cdp assert checked"] = []string{"cdp assert checked 'Subscribe to newsletter' --by label --target-index 2 --timeout 5s --json", "cdp assert checked Subscribe --by role --role checkbox --poll 100ms --json"}
	examples["cdp assert unchecked"] = []string{"cdp assert unchecked 'Subscribe to newsletter' --by label --target-index 2 --timeout 5s --json", "cdp assert unchecked '#subscribe' --poll 100ms --json"}
	examples["cdp assert indeterminate"] = []string{"cdp assert indeterminate '#partial-selection' --target-index 2 --timeout 5s --json", "cdp assert indeterminate 'Partial selection' --by role --role checkbox --poll 100ms --json"}
	for _, provider := range []string{"alex", "chatgpt", "claude", "gemini", "grok", "perplexity", "tripadvisor"} {
		command := "cdp workflow agent " + provider + " capabilities"
		examples[command] = []string{
			command + " --json",
			command + " --jq '.data.operations[] | select(.supported)'",
		}
	}
	examples["cdp workflow agent claude doctor"] = []string{
		"cdp workflow agent claude doctor --json",
		"cdp workflow agent claude auth refresh --json",
	}
	examples["cdp workflow agent claude auth"] = []string{
		"cdp workflow agent claude auth refresh --json",
	}
	examples["cdp workflow agent claude auth refresh"] = []string{
		"cdp workflow agent claude auth refresh --json",
		"cdp workflow agent claude doctor --json",
	}
	examples["cdp workflow agent claude ask"] = []string{
		"cdp workflow agent claude ask 'Review this design.' --json",
		"printf '%s' 'Review this diff.' | cdp workflow agent claude ask --stdin --json",
	}
	examples["cdp workflow agent claude conversations"] = []string{
		"cdp workflow agent claude conversations list --limit 30 --json",
		"cdp workflow agent claude conversations detail <conversation-id> --json",
		"cdp workflow agent claude conversations await <conversation-id> --json",
		"cdp workflow agent claude conversations delete <conversation-id> --json",
	}
	examples["cdp workflow agent claude conversations list"] = []string{
		"cdp workflow agent claude conversations list --limit 30 --json",
	}
	examples["cdp workflow agent claude conversations detail"] = []string{
		"cdp workflow agent claude conversations detail <conversation-id> --json",
	}
	examples["cdp workflow agent claude conversations await"] = []string{
		"cdp --timeout 3m workflow agent claude conversations await <conversation-id> --json",
	}
	examples["cdp workflow agent claude conversations delete"] = []string{
		"cdp workflow agent claude conversations delete <conversation-id> --json",
	}
	examples["cdp workflow agent gemini doctor"] = []string{
		"cdp workflow agent gemini doctor --json",
		"cdp workflow agent gemini auth refresh --json",
		"cdp workflow agent gemini capabilities refresh --json",
	}
	examples["cdp workflow agent gemini auth"] = []string{
		"cdp workflow agent gemini auth refresh --json",
	}
	examples["cdp workflow agent gemini auth refresh"] = []string{
		"cdp workflow agent gemini auth refresh --json",
		"cdp workflow agent gemini doctor --json",
	}
	examples["cdp workflow agent gemini ask"] = []string{
		"cdp workflow agent gemini ask 'Review this design.' --json",
		"printf '%s' 'Review this diff.' | cdp workflow agent gemini ask --stdin --json",
	}
	examples["cdp workflow agent gemini conversations"] = []string{
		"cdp workflow agent gemini conversations list --limit 30 --json",
		"cdp workflow agent gemini conversations detail <conversation-id> --json",
		"cdp workflow agent gemini conversations await <conversation-id> --json",
		"cdp workflow agent gemini conversations delete <conversation-id> --json",
	}
	examples["cdp workflow agent gemini conversations list"] = []string{
		"cdp workflow agent gemini conversations list --limit 30 --json",
	}
	examples["cdp workflow agent gemini conversations detail"] = []string{
		"cdp workflow agent gemini conversations detail <conversation-id> --json",
	}
	examples["cdp workflow agent gemini conversations await"] = []string{
		"cdp --timeout 3m workflow agent gemini conversations await <conversation-id> --json",
	}
	examples["cdp workflow agent gemini conversations delete"] = []string{
		"cdp workflow agent gemini conversations delete <conversation-id> --json",
	}

	return examples[path]
}

func findCommand(root *cobra.Command, path string) (*cobra.Command, error) {
	parts := strings.Fields(path)
	if len(parts) > 0 && parts[0] == root.Name() {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return root, nil
	}

	found, _, err := root.Find(parts)
	if err != nil || found == nil {
		return nil, commandError(
			"unknown_command",
			"usage",
			fmt.Sprintf("unknown command path %q", path),
			ExitUsage,
			[]string{"cdp describe --json", "cdp --help"},
		)
	}
	return found, nil
}
