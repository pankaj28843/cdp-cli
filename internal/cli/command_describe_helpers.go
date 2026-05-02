package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func planned(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := cmd.CommandPath()
			return notImplemented(name)
		},
	}
}

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
			"cdp daemon start --auto-connect --json",
			"cdp daemon start --browser-url <browser-url> --json",
			"cdp daemon start --autoConnect --json",
		},
		"cdp daemon status": {
			"cdp daemon status --json",
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
			"cdp daemon keepalive --auto-connect --display :0 --json",
			"cdp daemon keepalive --browser-url <browser-url> --json",
			"cdp daemon keepalive --connection default --probe auto --json",
		},
		"cdp daemon logs": {
			"cdp daemon logs --tail 100 --json",
			"cdp daemon logs --tail 0 --json",
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
			"cdp connection resolve --connection default --json",
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
			"cdp page cleanup --json",
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
		"cdp click": {
			"cdp click 'button.submit' --json",
			"cdp click '[data-testid=row]' --strategy raw-input --activate --wait-text 'Opened' --timeout 10s --json",
			"cdp click 'button.submit' --wait-selector '.toast-success' --diagnostics-out tmp/click.local.json --json",
		},
		"cdp text": {
			"cdp text main --json",
			"cdp text article --limit 10 --url-contains localhost --json",
		},
		"cdp fill": {
			"cdp fill input[name='email'] user@example.com --json",
			"cdp fill textarea#notes \"first line\\nsecond line\" --url-contains localhost --json",
		},
		"cdp type": {
			"cdp type input[name='email'] user@example.com --json",
			"cdp type textarea#notes \"typed characters\" --url-contains localhost --json",
		},
		"cdp press": {
			"cdp press Enter --json",
			"cdp press Tab --selector 'input[name=\"q\"]' --json",
		},
		"cdp hover": {
			"cdp hover button.primary --json",
			"cdp hover '.card' --url-contains localhost --json",
		},
		"cdp drag": {
			"cdp drag '.draggable' 10 20 --json",
			"cdp drag '#drag-handle' -8 12 --url-contains localhost --json",
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
		"cdp snapshot": {
			"cdp snapshot --selector body --json",
			"cdp snapshot --selector article --limit 10 --url-contains x.com --json",
			"cdp snapshot --selector body --diagnose-empty --json",
		},
		"cdp screenshot": {
			"cdp screenshot --out tmp/page.png --json",
			"cdp screenshot --target <target-id> --full-page --out tmp/page.png --json",
			"cdp screenshot --url-contains localhost --out tmp/page.png --json",
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
			"cdp storage cookies list --url https://example.com --json",
		},
		"cdp storage cookies list": {
			"cdp storage cookies list --url-contains localhost --json",
		},
		"cdp storage cookies set": {
			"cdp storage cookies set --url https://example.com --name feature_flag --value enabled --json",
		},
		"cdp storage cookies delete": {
			"cdp storage cookies delete --url https://example.com --name feature_flag --json",
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
			"cdp workflow verify https://example.com --json",
		},
		"cdp workflow debug-bundle": {
			"cdp workflow debug-bundle --url https://example.com --since 5s --screenshot-view --out-dir tmp/debug-bundle --json",
			"cdp workflow debug-bundle --target <target-id> --out-dir tmp/debug-bundle --json",
		},
		"cdp workflow a11y": {
			"cdp workflow a11y https://example.com --wait 5s --json",
			"cdp workflow a11y https://example.com --limit 50 --wait 5s --json",
		},
		"cdp workflow visible-posts": {
			"cdp workflow visible-posts https://x.com/<handle> --limit 5 --json",
			"cdp workflow visible-posts https://example.com/feed --selector article --wait 30s --json",
		},
		"cdp workflow hacker-news": {
			"cdp workflow hacker-news --limit 10 --json",
			"cdp workflow hacker-news https://news.ycombinator.com/news --wait 30s --json",
		},
		"cdp workflow perf": {
			"cdp workflow perf https://example.com --wait 5s --json",
			"cdp workflow perf https://example.com --wait 5s --trace tmp/perf.local.json --json",
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
			"cdp workflow page-load https://example.com --wait 10s --json",
			"cdp workflow page-load --url-contains localhost --reload --include console,network,performance --out tmp/page-load.local.json --json",
		},
		"cdp workflow rendered-extract": {
			"cdp workflow rendered-extract https://example.com --out-dir tmp/rendered-example --json",
			"cdp workflow rendered-extract 'https://www.google.com/search?q=agentic+engineering&safe=active&tbs=qdr:m' --serp google --out-dir tmp/rendered-google --json",
		},
		"cdp workflow web-research": {
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --out-dir tmp/research --json",
			"cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --parallel 10 --out-dir tmp/research/pages --json",
		},
		"cdp workflow web-research serp": {
			"cdp workflow web-research serp --query-file tmp/research/queries.txt --result-pages 3 --max-candidates 200 --candidate-out tmp/research/candidates.json --out-dir tmp/research --json",
		},
		"cdp workflow web-research extract": {
			"cdp workflow web-research extract --url-file tmp/research/visit-urls.txt --max-pages 100 --parallel 10 --out-dir tmp/research/pages --json",
		},
	}
	examples["cdp focus"] = []string{"cdp focus input[name=email] --json"}
	examples["cdp clear"] = []string{"cdp clear input[name=email] --json"}
	examples["cdp select"] = []string{"cdp select select[name=plan] pro --json"}
	examples["cdp file"] = []string{"cdp file input[type=file] tmp/upload.txt --json"}
	examples["cdp dialog"] = []string{"cdp dialog wait --timeout 5s --json"}
	examples["cdp dialog wait"] = []string{"cdp dialog wait --timeout 5s --json"}
	examples["cdp dialog accept"] = []string{"cdp dialog accept --prompt-text yes --json"}
	examples["cdp dialog dismiss"] = []string{"cdp dialog dismiss --json"}
	examples["cdp emulate"] = []string{"cdp emulate viewport --preset mobile --json"}
	examples["cdp emulate viewport"] = []string{"cdp emulate viewport --width 390 --height 844 --mobile --dpr 1 --json", "cdp emulate viewport --preset iphone-12 --json"}
	examples["cdp emulate clear"] = []string{"cdp emulate clear --json"}
	examples["cdp emulate media"] = []string{"cdp emulate media --prefers-color-scheme dark --json"}
	examples["cdp emulate network"] = []string{"cdp emulate network --preset slow-4g --json"}
	examples["cdp emulate cpu"] = []string{"cdp emulate cpu --rate 4 --json"}
	examples["cdp emulate geolocation"] = []string{"cdp emulate geolocation --lat 55.6 --lon 12.5 --json"}
	examples["cdp a11y"] = []string{"cdp a11y tree --depth 4 --json"}
	examples["cdp a11y tree"] = []string{"cdp a11y tree --target <target-id> --depth 4 --json"}
	examples["cdp a11y find"] = []string{"cdp a11y find --role button --name Save --json"}
	examples["cdp a11y node"] = []string{"cdp a11y node button[type=submit] --json"}
	examples["cdp perf summary"] = []string{"cdp perf summary --duration 5s --json"}
	examples["cdp memory counters"] = []string{"cdp memory counters --json"}
	examples["cdp memory heap-snapshot"] = []string{"cdp memory heap-snapshot --out tmp/page.heapsnapshot --json"}
	examples["cdp events"] = []string{"cdp events tap --duration 10s --json"}
	examples["cdp events tap"] = []string{"cdp events tap --enable page,network,runtime --match Page.lifecycleEvent,Network.loadingFailed --duration 10s --json"}
	examples["cdp network block"] = []string{"cdp network block --url-pattern '*.analytics.test/*' --json"}
	examples["cdp network unblock"] = []string{"cdp network unblock --json"}
	examples["cdp network mock"] = []string{"cdp network mock --url-pattern '*/api/feed' --status 503 --body-file fixtures/feed-503.json --json"}
	examples["cdp protocol compat"] = []string{"cdp protocol compat --requires Target.attachToTarget,Runtime.evaluate --json", "cdp protocol compat --workflow debug-bundle --json"}
	examples["cdp wait load"] = []string{"cdp wait load --state load --timeout 10s --json"}
	examples["cdp wait stable"] = []string{"cdp wait stable --quiet-window 750ms --timeout 10s --json"}
	examples["cdp wait idle"] = []string{"cdp wait idle --quiet-window 500ms --max-inflight 0 --timeout 10s --json"}
	examples["cdp workflow feeds"] = []string{"cdp workflow feeds https://example.com --wait-load 10s --json", "cdp workflow feeds https://example.com --keep-open --json"}
	examples["cdp workflow responsive-audit"] = []string{"cdp workflow responsive-audit https://example.com --viewports desktop,tablet,mobile --json"}
	examples["cdp workflow perf-smoke"] = []string{"cdp workflow perf-smoke https://example.com --out-dir tmp/perf-smoke --json"}
	examples["cdp workflow memory-smoke"] = []string{"cdp workflow memory-smoke https://example.com --out-dir tmp/memory-smoke --json"}

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
