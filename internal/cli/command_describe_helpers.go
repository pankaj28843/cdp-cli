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
		"cdp daemon status": {
			"cdp daemon status --json",
			"cdp daemon health --json",
		},
		"cdp daemon stop": {
			"cdp daemon stop --json",
		},
		"cdp daemon restart": {
			"cdp daemon restart --auto-connect --json",
			"cdp daemon restart --debug --autoConnect --active-browser-probe --json",
			"cdp daemon restart --browser-url <browser-url> --json",
		},
		"cdp daemon keepalive": {
			"cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe passive --reconnect 30s --display :0 --json",
			"cdp --browser-mode headed daemon keepalive --auto-connect --repair --probe active --reconnect 30s --display :0 --json",
			"cdp --browser-mode headless daemon keepalive --repair --reconnect 30s --json",
			"cdp cron install --profile agent --json",
			"cdp daemon keepalive --browser-url <browser-url> --json",
			"cdp --connection default daemon keepalive --probe auto --json",
		},
		"cdp daemon health-check": {
			"cdp --browser-mode headless daemon health-check --repair --json",
			"cdp --browser-mode headless daemon health-check --repair --failure-threshold 1 --json",
			"cdp --browser-mode headless daemon health-check --out-dir tmp/headless-health --json",
		},
		"cdp daemon logs": {
			"cdp daemon logs --tail 100 --json",
			"cdp daemon logs --tail 0 --json",
		},
		"cdp cron": {
			"cdp cron status --json",
			"cdp cron install --profile agent --json",
			"cdp cron migrate pages-polling --json",
		},
		"cdp cron status": {
			"cdp cron status --json",
			"cdp cron diff --json",
		},
		"cdp cron install": {
			"cdp cron install --profile agent --json",
			"cdp --browser-mode headed cron install --dry-run --json",
			"cdp --browser-mode headless cron install --dry-run --json",
			"cdp cron install --profile agent --cdp-bin $HOME/.local/bin/cdp --json",
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
			"cdp browser mode get --json",
			"cdp browser profile status --json",
			"CDP_BROWSER_MODE=headless cdp browser mode get --json",
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
			"cdp targets --limit 10 --json",
			"cdp targets --type service_worker --json",
		},
		"cdp pages": {
			"cdp pages --json",
			"cdp pages --limit 10 --json",
			"cdp pages --include-url localhost --exclude-url admin --json",
			"cdp pages --title-contains Example --json",
		},
		"cdp page select": {
			"cdp page select <target-id> --json",
			"cdp page select --url-contains localhost --json",
		},
		"cdp page reload": {
			"cdp page reload --target <target-id> --json",
			"cdp page reload --url-contains localhost --ignore-cache --json",
		},
		"cdp page back": {
			"cdp page back --target <target-id> --json",
		},
		"cdp page forward": {
			"cdp page forward --target <target-id> --json",
		},
		"cdp page activate": {
			"cdp page activate --target <target-id> --json",
		},
		"cdp page close": {
			"cdp page close --target <target-id> --json",
		},
		"cdp page cleanup": {
			"cdp --browser-mode headed page cleanup --json",
			"cdp --browser-mode headless page cleanup --created-by cdp --idle-for 30m --close --force --max 25 --json",
			"cdp page cleanup --workflow-created --close --include-url localhost --json",
			"cdp page cleanup --target <target-id> --force --json",
			"cdp page cleanup --close --max 10 --exclude-url localhost --json",
		},
		"cdp open": {
			"cdp open https://example.com --json",
			"cdp open https://example.com --new-tab=false --target <target-id> --json",
		},
		"cdp eval": {
			"cdp eval 'document.title' --json",
			"cdp eval 'Array.from(document.querySelectorAll(\"article\"), el => el.innerText)' --url-contains x.com --json",
			"cdp eval 'document.title' --title-contains Example --json",
		},
		"cdp observe": {
			"cdp observe --json",
			"cdp observe --selector 'button, a[href], input' --limit 30 --json",
		},
		"cdp click": {
			"cdp click 'button.submit' --json",
			"cdp click 'Search' --by role --role button --json",
			"cdp click 'Search' --by role --role button --trial --json",
			"cdp click 'button.covered' --force --json",
			"cdp click '[data-testid=row]' --strategy raw-input --activate --wait-text 'Opened' --timeout 10s --json",
			"cdp click 'Sign in' --by role --role link --wait-popup --wait-popup-url '/oauth' --json",
			"cdp click 'Download report' --by role --role link --wait-download --wait-download-filename report --download-dir tmp/downloads --json",
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
			"cdp text article --limit 10 --url-contains localhost --json",
		},
		"cdp locator": {
			"cdp locator find Search --by label --json",
			"cdp locator find Submit --by role --role button --json",
		},
		"cdp locator find": {
			"cdp locator find Search --by label --json",
			"cdp locator find Submit --by role --role button --json",
			"cdp locator find cdp_demo --by test-id --test-id-attr data-testid --json",
		},
		"cdp fill": {
			"cdp fill input[name='email'] user@example.com --json",
			"cdp fill 'Search Wikipedia' Aarhus --by label --json",
			"cdp fill 'Search Wikipedia' Aarhus --by label --wait-selector '.suggestions' --json",
			"cdp fill 'Search Wikipedia' Aarhus --by label --wait-url-contains /search --json",
			"cdp fill 'Search Wikipedia' Aarhus --by label --trial --json",
			"cdp fill 'input[type=hidden][name=token]' value --force --json",
			"cdp fill textarea#notes \"first line\\nsecond line\" --url-contains localhost --json",
		},
		"cdp type": {
			"cdp type input[name='email'] user@example.com --json",
			"cdp type 'Search Wikipedia' Aarhus --by label --json",
			"cdp type 'Search Wikipedia' Aarhus --by label --wait-text Results --json",
			"cdp type 'Search Wikipedia' Aarhus --by label --wait-url-contains /search --json",
			"cdp type 'Search Wikipedia' Aarhus --by label --trial --json",
			"cdp type 'input[type=hidden][name=token]' value --force --json",
			"cdp type textarea#notes \"typed characters\" --url-contains localhost --json",
		},
		"cdp press": {
			"cdp press Enter --json",
			"cdp press Tab --selector 'input[name=\"q\"]' --json",
			"cdp press Enter 'Search Wikipedia' --by label --json",
			"cdp press Enter 'Search Wikipedia' --by label --wait-text Results --json",
			"cdp press Enter 'Search Wikipedia' --by label --wait-url-contains /search --json",
			"cdp press Enter 'Search Wikipedia' --by label --trial --json",
		},
		"cdp hover": {
			"cdp hover button.primary --json",
			"cdp hover 'Save changes' --by role --role button --trial --json",
			"cdp hover '#covered-action' --force --url-contains localhost --json",
		},
		"cdp drag": {
			"cdp drag '.draggable' 10 20 --json",
			"cdp drag drag-target 8 12 --by test-id --trial --json",
			"cdp drag '#drag-handle' -8 12 --force --url-contains localhost --json",
		},
		"cdp frames": {
			"cdp frames --json",
			"cdp frames --url-contains localhost --json",
		},
		"cdp html": {
			"cdp html main --max-chars 4000 --json",
			"cdp html '#root' --limit 1 --json",
			"cdp html body --diagnose-empty --json",
		},
		"cdp dom query": {
			"cdp dom query button --json",
			"cdp dom query '[role=\"button\"]' --limit 20 --json",
		},
		"cdp css inspect": {
			"cdp css inspect main --json",
			"cdp css inspect '.panel' --url-contains localhost --json",
		},
		"cdp layout overflow": {
			"cdp layout overflow --json",
			"cdp layout overflow --selector 'body *' --limit 20 --json",
		},
		"cdp wait text": {
			"cdp wait text Ready --timeout 10s --json",
			"cdp wait text 'Dashboard loaded' --url-contains localhost --json",
		},
		"cdp wait selector": {
			"cdp wait selector main --timeout 10s --json",
			"cdp wait selector '[data-ready=\"true\"]' --poll 500ms --json",
		},
		"cdp wait url": {
			"cdp wait url /results --mode contains --timeout 10s --json",
			"cdp wait url https://example.com/checkout --mode exact --poll 100ms --json",
		},
		"cdp wait locator": {
			"cdp wait locator 'Search' --by role --role button --strict --timeout 10s --json",
			"cdp wait locator 'Dashboard loaded' --by text --timeout 10s --json",
		},
		"cdp wait eval": {
			"cdp wait eval 'window.__rendered === true' --timeout 10s --json",
			"cdp wait eval 'document.readyState === \"complete\"' --poll 500ms --json",
		},
		"cdp wait load-state": {
			"cdp wait load-state load --timeout 10s --json",
			"cdp wait load-state domcontentloaded --poll 100ms --json",
		},
		"cdp wait request": {
			"cdp wait request --match-url /api/search --method POST --timeout 10s --json",
			"cdp wait request --url https://example.com/api --resource-type Fetch --json",
		},
		"cdp wait response": {
			"cdp wait response --match-url /api/search --status 200 --timeout 10s --json",
			"cdp wait response --method GET --status-min 200 --status-max 399 --json",
		},
		"cdp wait network-idle": {
			"cdp wait network-idle --idle 500ms --timeout 10s --json",
			"cdp wait network-idle --ignore-url-contains /events --idle 1s --json",
		},
		"cdp wait dialog": {
			"cdp wait dialog --type confirm --action dismiss --timeout 10s --json",
			"cdp wait dialog --message-contains 'Delete item?' --action accept --json",
		},
		"cdp wait file-chooser": {
			"cdp wait file-chooser --mode single --timeout 10s --json",
			"cdp wait file-chooser --mode multiple --json",
		},
		"cdp wait popup": {
			"cdp wait popup --match-url /oauth/callback --timeout 10s --json",
			"cdp wait popup --title-contains Checkout --match-title Receipt --json",
		},
		"cdp wait download": {
			"cdp wait download --match-url /report.csv --filename-contains report --download-dir tmp/downloads --json",
			"cdp wait download --state started --timeout 10s --json",
		},
		"cdp snapshot": {
			"cdp snapshot --selector body --json",
			"cdp snapshot --selector article --limit 10 --url-contains x.com --json",
			"cdp snapshot --selector body --diagnose-empty --json",
		},
		"cdp screenshot": {
			"cdp screenshot --out tmp/page.png --json",
			"cdp screenshot --preset mobile --out tmp/mobile.png --json",
			"cdp screenshot --target <target-id> --full-page --out tmp/page.png --json",
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
			"cdp console --url-contains localhost --types error,warning --json",
		},
		"cdp network": {
			"cdp network --wait 2s --json",
			"cdp network --failed --url-contains localhost --json",
		},
		"cdp network capture": {
			"cdp network capture --reload --wait 20s --out tmp/network.local.json --json",
			"cdp network capture --redact safe --har-out tmp/network.har --json",
			"cdp network capture --include-websockets --include-websocket-payloads --out tmp/network-with-ws.local.json --json",
			"cdp network capture --url-contains localhost --redact safe --out tmp/network-shareable.json --json",
		},
		"cdp network websocket": {
			"cdp network websocket --wait 20s --include-payloads --out tmp/ws.local.json --json",
			"cdp network websocket --url-contains localhost --redact safe --json",
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
			"cdp protocol exec Page.captureScreenshot --target <target-id> --params '{\"format\":\"png\"}' --save tmp/page.png --json",
			"cdp protocol exec DOM.getDocument --url-contains localhost --json",
		},
		"cdp workflow verify": {
			"cdp workflow verify 'https://example.com' --json",
		},
		"cdp workflow debug-bundle": {
			"cdp workflow debug-bundle --url 'https://example.com' --since 5s --screenshot-view --out-dir tmp/debug-bundle --json",
			"cdp workflow debug-bundle --target <target-id> --out-dir tmp/debug-bundle --json",
		},
		"cdp workflow action-capture": {
			"cdp workflow action-capture --action click:'button.submit' --include network,console,dom,text,a11y,screenshot --wait-after 2s --evidence-out-dir tmp/action-capture --json",
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
		"cdp workflow perf": {
			"cdp workflow perf 'https://example.com' --wait 5s --json",
			"cdp workflow perf 'https://example.com' --wait 5s --trace tmp/perf.local.json --json",
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
		},
		"cdp workflow rendered-extract": {
			"cdp workflow rendered-extract 'https://example.com' --out-dir tmp/rendered-example --json",
			"cdp workflow rendered-extract 'https://www.google.com/search?q=agentic+engineering&safe=active&tbs=qdr:m' --serp google --out-dir tmp/rendered-google --json",
		},
		"cdp workflow web-research": {
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --out-dir tmp/research --json",
			"cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --out-dir tmp/research/pages --json",
		},
		"cdp workflow web-research serp": {
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --serp google --result-pages 3 --max-candidates 200 --candidate-out tmp/research/candidates.json --out-dir tmp/research --json",
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --serp all --parallel-engines --result-pages 2 --out-dir tmp/research-all --json",
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --serp duckduckgo --fallback-serp google --result-pages 2 --out-dir tmp/research-ddg --json",
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --serp bing --result-pages 3 --fast-fail-blocked --blocked-failure-threshold 3 --progress stderr --json",
		},
		"cdp workflow web-research extract": {
			"cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --max-pages 100 --parallel 4 --out-dir tmp/research/pages --json",
			"cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --parallel 10 --allow-over-budget --json # supervised high-stress cap",
		},
	}
	examples["cdp focus"] = []string{"cdp focus input[name=email] --json"}
	examples["cdp clear"] = []string{"cdp clear input[name=email] --json"}
	examples["cdp select"] = []string{"cdp select select[name=plan] pro --json", "cdp select Plan pro --by label --wait-text 'Plan selected' --json", "cdp select Plan pro --by label --trial --json", "cdp select '#hidden-plan' pro --force --json"}
	examples["cdp check"] = []string{"cdp check 'Subscribe to newsletter' --by label --json", "cdp check Subscribe --by role --role checkbox --trial --json", "cdp check '#covered-checkbox' --force --json"}
	examples["cdp uncheck"] = []string{"cdp uncheck 'Subscribe to newsletter' --by label --json", "cdp uncheck Subscribe --by role --role checkbox --trial --json", "cdp uncheck '#covered-checkbox' --force --json"}
	examples["cdp file"] = []string{"cdp file input[type=file] tmp/upload.txt --json", "cdp file 'Upload file' tmp/upload.txt --by label --trial --json"}
	examples["cdp scroll"] = []string{"cdp scroll '#results' --json", "cdp scroll 'Load more' --by role --role button --trial --json", "cdp scroll 'footer' --block end --inline nearest --json"}
	examples["cdp dialog"] = []string{"cdp dialog accept --prompt-text yes --json", "cdp dialog dismiss --json"}
	examples["cdp dialog accept"] = []string{"cdp dialog accept --prompt-text yes --json"}
	examples["cdp dialog dismiss"] = []string{"cdp dialog dismiss --json"}
	examples["cdp emulate"] = []string{"cdp emulate viewport --preset mobile --json", "cdp emulate user-agent --user-agent 'Mozilla/5.0 ...' --json", "cdp emulate network --preset slow-3g --json"}
	examples["cdp emulate viewport"] = []string{"cdp emulate viewport --width 390 --height 844 --mobile --dpr 1 --json", "cdp emulate viewport --preset iphone-12 --json"}
	examples["cdp emulate clear"] = []string{"cdp emulate clear --json"}
	examples["cdp emulate media"] = []string{"cdp emulate media --prefers-color-scheme dark --json"}
	examples["cdp emulate user-agent"] = []string{"cdp emulate user-agent --user-agent 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/125 Safari/537.36' --platform Linux --json"}
	examples["cdp emulate geolocation"] = []string{"cdp emulate geolocation --latitude 55.6761 --longitude 12.5683 --accuracy 100 --json"}
	examples["cdp emulate cpu"] = []string{"cdp emulate cpu --rate 4 --json", "cdp emulate cpu --rate 1 --json"}
	examples["cdp emulate network"] = []string{"cdp emulate network --preset slow-3g --json", "cdp emulate network --latency 100 --download-kbps 750 --upload-kbps 250 --json", "cdp emulate network --preset none --json"}
	examples["cdp a11y"] = []string{"cdp a11y tree --depth 4 --json"}
	examples["cdp a11y tree"] = []string{"cdp a11y tree --target <target-id> --depth 4 --json"}
	examples["cdp a11y find"] = []string{"cdp a11y find --role button --name Save --json"}
	examples["cdp a11y node"] = []string{"cdp a11y node button[type=submit] --json"}
	examples["cdp perf summary"] = []string{"cdp perf summary --duration 5s --json"}
	examples["cdp memory counters"] = []string{"cdp memory counters --json"}
	examples["cdp memory heap-snapshot"] = []string{"cdp memory heap-snapshot --out tmp/page.heapsnapshot --json"}
	examples["cdp events"] = []string{"cdp events tap --duration 10s --json"}
	examples["cdp events tap"] = []string{"cdp events tap --enable page,network,runtime --match Page.lifecycleEvent,Network.loadingFailed --duration 10s --json"}
	examples["cdp protocol compat"] = []string{"cdp protocol compat --requires Target.attachToTarget,Runtime.evaluate --json", "cdp protocol compat --workflow debug-bundle --json"}
	examples["cdp workflow feeds"] = []string{"cdp workflow feeds 'https://example.com' --wait-load 10s --json", "cdp workflow feeds 'https://example.com' --keep-open --json"}
	examples["cdp workflow responsive-audit"] = []string{"cdp workflow responsive-audit 'https://example.com' --viewports desktop,tablet,mobile --include console,network,layout,screenshot,a11y --json"}
	examples["cdp workflow lighthouse"] = []string{"cdp workflow lighthouse 'https://example.com' --categories accessibility,best-practices --out-dir tmp/lighthouse --json"}
	examples["cdp form values"] = []string{"cdp form values --url-contains localhost --json"}
	examples["cdp form get"] = []string{"cdp form get 'textarea[aria-label=Output]' --json"}
	examples["cdp assert value"] = []string{"cdp assert value 'textarea[aria-label=Output]' expected --mode exact --timeout 5s --json", "cdp assert value 'Search' expected --by label --poll 100ms --json"}
	examples["cdp assert text"] = []string{"cdp assert text 'Saved successfully' --mode contains --timeout 5s --json", "cdp assert text 'Search' 'Search' --by role --role button --poll 100ms --json"}
	examples["cdp assert url"] = []string{"cdp assert url example.com --mode contains --timeout 5s --json", "cdp assert url '^https://example\\.com/' --mode regex --poll 100ms --json"}
	examples["cdp assert title"] = []string{"cdp assert title 'Example Domain' --mode exact --timeout 5s --json", "cdp assert title Checkout --mode contains --poll 100ms --json"}
	examples["cdp assert count"] = []string{"cdp assert count '.result-item' 10 --timeout 5s --json", "cdp assert count 'Search result' 3 --by role --role listitem --poll 100ms --json"}
	examples["cdp assert attribute"] = []string{"cdp assert attribute 'button[type=submit]' data-state ready --mode exact --timeout 5s --json", "cdp assert attribute Checkout aria-expanded true --by role --role button --poll 100ms --json"}
	examples["cdp assert class"] = []string{"cdp assert class 'button[type=submit]' primary --timeout 5s --json", "cdp assert class Checkout primary --by role --role button --poll 100ms --json"}
	examples["cdp assert focused"] = []string{"cdp assert focused 'input[name=q]' --timeout 5s --json", "cdp assert focused 'Search' --by label --poll 100ms --json"}
	examples["cdp assert css"] = []string{"cdp assert css 'button[type=submit]' background-color 'rgb(20, 92, 160)' --mode exact --timeout 5s --json", "cdp assert css Checkout color 'rgb(255, 255, 255)' --by role --role button --poll 100ms --json"}
	examples["cdp assert role"] = []string{"cdp assert role 'button[type=submit]' button --timeout 5s --json", "cdp assert role Checkout button --by role --role button --poll 100ms --json"}
	examples["cdp assert name"] = []string{"cdp assert name 'button[type=submit]' Submit --mode exact --timeout 5s --json", "cdp assert name Checkout Checkout --by role --role button --poll 100ms --json"}
	examples["cdp assert attached"] = []string{"cdp assert attached '#app' --timeout 5s --json", "cdp assert attached 'Search' --by role --role button --poll 100ms --json"}
	examples["cdp assert detached"] = []string{"cdp assert detached '#loading-spinner' --timeout 5s --json", "cdp assert detached 'Gone' --by text --poll 100ms --json"}
	examples["cdp assert visible"] = []string{"cdp assert visible 'button[type=submit]' --timeout 5s --json", "cdp assert visible 'Search' --by role --role button --poll 100ms --json"}
	examples["cdp assert hidden"] = []string{"cdp assert hidden '#loading-spinner' --timeout 5s --json", "cdp assert hidden 'Dismiss' --by role --role button --poll 100ms --json"}
	examples["cdp assert in-viewport"] = []string{"cdp assert in-viewport '#footer' --timeout 5s --json", "cdp assert in-viewport 'Load more' --by role --role button --poll 100ms --json"}
	examples["cdp assert enabled"] = []string{"cdp assert enabled 'button[type=submit]' --timeout 5s --json", "cdp assert enabled 'Search' --by role --role button --poll 100ms --json"}
	examples["cdp assert disabled"] = []string{"cdp assert disabled 'button[disabled]' --timeout 5s --json", "cdp assert disabled 'Submit' --by role --role button --poll 100ms --json"}
	examples["cdp assert editable"] = []string{"cdp assert editable 'input[name=email]' --timeout 5s --json", "cdp assert editable 'Search' --by label --poll 100ms --json"}
	examples["cdp assert readonly"] = []string{"cdp assert readonly 'textarea[readonly]' --timeout 5s --json", "cdp assert readonly 'Read-only notes' --by label --poll 100ms --json"}
	examples["cdp assert checked"] = []string{"cdp assert checked 'Subscribe to newsletter' --by label --timeout 5s --json", "cdp assert checked Subscribe --by role --role checkbox --poll 100ms --json"}
	examples["cdp assert unchecked"] = []string{"cdp assert unchecked 'Subscribe to newsletter' --by label --timeout 5s --json", "cdp assert unchecked '#subscribe' --poll 100ms --json"}
	examples["cdp assert indeterminate"] = []string{"cdp assert indeterminate '#partial-selection' --timeout 5s --json", "cdp assert indeterminate 'Partial selection' --by role --role checkbox --poll 100ms --json"}

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
