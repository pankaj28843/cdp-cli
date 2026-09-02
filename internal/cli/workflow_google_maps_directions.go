package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pankaj28843/cdp-cli/internal/cdp"
	"github.com/spf13/cobra"
)

type googleMapsRouteCard struct {
	Text      string `json:"text"`
	AriaLabel string `json:"aria_label,omitempty"`
	Role      string `json:"role,omitempty"`
}

type googleMapsRoute struct {
	Index           int                       `json:"index"`
	DurationText    string                    `json:"duration_text"`
	DurationMinutes int                       `json:"duration_minutes"`
	DistanceText    string                    `json:"distance_text,omitempty"`
	DistanceKM      float64                   `json:"distance_km,omitempty"`
	TimeWindowText  string                    `json:"time_window_text,omitempty"`
	DepartureTime   string                    `json:"departure_time,omitempty"`
	DepartureDay    string                    `json:"departure_day,omitempty"`
	ArrivalTime     string                    `json:"arrival_time,omitempty"`
	ArrivalDay      string                    `json:"arrival_day,omitempty"`
	Name            string                    `json:"route_name,omitempty"`
	Summary         string                    `json:"summary"`
	Incidents       []googleMapsRouteIncident `json:"incidents"`
}

type googleMapsRouteIncident struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Text     string `json:"text"`
}

type googleMapsEndpointIdentity struct {
	Requested     string   `json:"requested"`
	RequestedName string   `json:"requested_name"`
	Resolved      string   `json:"resolved,omitempty"`
	State         string   `json:"state"`
	MissingTokens []string `json:"missing_tokens"`
}

type googleMapsRouteTrust struct {
	Level   string   `json:"level"`
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

type googleMapsDirectionsExtraction struct {
	Title             string                `json:"title"`
	URL               string                `json:"url"`
	VisibleTextLength int                   `json:"visible_text_length"`
	PageState         string                `json:"page_state"`
	OriginLabels      []string              `json:"origin_labels"`
	DestinationLabels []string              `json:"destination_labels"`
	Cards             []googleMapsRouteCard `json:"cards"`
}

type googleMapsDirectionsEvidence struct {
	TargetID          string `json:"target_id"`
	PageTitle         string `json:"page_title,omitempty"`
	FinalURL          string `json:"final_url,omitempty"`
	VisibleTextLength int    `json:"visible_text_length"`
	AttemptCount      int    `json:"attempt_count"`
	ElapsedMS         int64  `json:"elapsed_ms"`
	Bounded           bool   `json:"bounded"`
}

var (
	googleMapsDurationPattern       = regexp.MustCompile(`(?i)\b(?:(\d+)\s*(?:h|hr|hrs|hour|hours)\s*)?(?:(\d+)\s*(?:min|mins|minute|minutes))\b|\b(\d+)\s*(?:h|hr|hrs|hour|hours)\b`)
	googleMapsDistancePattern       = regexp.MustCompile(`(?i)\b(\d+(?:[.,]\d+)?)\s*(km|mi|mile|miles)\b|\b(\d+(?:[.,]\d+)?)\s+m\b`)
	googleMapsTimeWindowPattern     = regexp.MustCompile(`(?i)\b(\d{1,2}:\d{2}\s*(?:AM|PM))(?:\s*\(([^)]+)\))?\s*[—–-]+\s*(\d{1,2}:\d{2}\s*(?:AM|PM))(?:\s*\(([^)]+)\))?`)
	googleMapsViaPattern            = regexp.MustCompile(`(?i)\bvia\s+(.+)$`)
	googleMapsNameStopPattern       = regexp.MustCompile(`(?i)\b(?:Road closure|Fastest route|Details|Preview|Explore|Leave now|Options|Copy link|Send directions|Restaurants|Hotels|Gas stations|Parking Lots|More|New!)\b`)
	googleMapsRoadClosurePattern    = regexp.MustCompile(`(?i)\b(?:road closure|road closed|closed road|partial closure)\b`)
	googleMapsTokenSeparatorPattern = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

func parseGoogleMapsRouteCards(cards []googleMapsRouteCard) []googleMapsRoute {
	return parseGoogleMapsRouteCardsForMode(cards, "driving")
}

func parseGoogleMapsRouteCardsForMode(cards []googleMapsRouteCard, travelMode string) []googleMapsRoute {
	routes := make([]googleMapsRoute, 0, len(cards))
	indexes := map[string]int{}
	scores := []int{}
	for _, card := range cards {
		text := strings.Join(strings.Fields(card.Text), " ")
		durationMatch := googleMapsDurationPattern.FindStringSubmatch(text)
		distanceMatch := googleMapsDistancePattern.FindStringSubmatch(text)
		timeWindowMatch := googleMapsTimeWindowPattern.FindStringSubmatch(text)
		if len(durationMatch) == 0 || travelMode == "driving" && len(distanceMatch) == 0 || travelMode == "transit" && len(timeWindowMatch) == 0 {
			continue
		}
		duration := parseGoogleMapsDuration(durationMatch)
		distance, distanceText := 0.0, ""
		if len(distanceMatch) > 0 {
			distance = parseGoogleMapsDistance(distanceMatch)
			distanceText = distanceMatch[0]
		}
		if duration <= 0 || travelMode == "driving" && distance <= 0 {
			continue
		}
		name := ""
		if via := googleMapsViaPattern.FindStringSubmatch(text); len(via) > 1 {
			name = strings.TrimSpace(googleMapsNameStopPattern.Split(via[1], 2)[0])
		}
		timeWindow, departureTime, departureDay, arrivalTime, arrivalDay := "", "", "", "", ""
		if len(timeWindowMatch) > 0 {
			timeWindow = strings.TrimSpace(timeWindowMatch[0])
			departureTime = strings.TrimSpace(timeWindowMatch[1])
			departureDay = strings.TrimSpace(timeWindowMatch[2])
			arrivalTime = strings.TrimSpace(timeWindowMatch[3])
			arrivalDay = strings.TrimSpace(timeWindowMatch[4])
		}
		key := fmt.Sprintf("%d/%.3f/%s/%s", duration, distance, strings.ToLower(name), strings.ToLower(timeWindow))
		candidate := googleMapsRoute{
			DurationText:    durationMatch[0],
			DurationMinutes: duration,
			DistanceText:    distanceText,
			DistanceKM:      distance,
			TimeWindowText:  timeWindow,
			DepartureTime:   departureTime,
			DepartureDay:    departureDay,
			ArrivalTime:     arrivalTime,
			ArrivalDay:      arrivalDay,
			Name:            name,
			Summary:         truncateGoogleMapsText(text, 260),
			Incidents:       googleMapsIncidents(text),
		}
		score := googleMapsRouteCardScore(card, text)
		if index, exists := indexes[key]; exists {
			if score < scores[index] {
				candidate.Index = index
				routes[index] = candidate
				scores[index] = score
			}
			continue
		}
		candidate.Index = len(routes)
		indexes[key] = candidate.Index
		routes = append(routes, candidate)
		scores = append(scores, score)
	}
	filtered := make([]googleMapsRoute, 0, len(routes))
	for index, route := range routes {
		if route.Name == "" {
			hasNamedEquivalent := false
			for otherIndex, other := range routes {
				if otherIndex != index && other.Name != "" && other.DurationMinutes == route.DurationMinutes && other.DistanceKM == route.DistanceKM {
					hasNamedEquivalent = true
					break
				}
			}
			if hasNamedEquivalent {
				continue
			}
		}
		route.Index = len(filtered)
		filtered = append(filtered, route)
	}
	return filtered
}

func googleMapsRouteCardScore(card googleMapsRouteCard, text string) int {
	score := len(text)
	if len(text) > 400 {
		score += 1000
	}
	role := strings.ToLower(strings.TrimSpace(card.Role))
	if role == "button" || role == "link" {
		score -= 500
	}
	if strings.TrimSpace(card.AriaLabel) != "" {
		score -= 100
	}
	return score
}

func matchGoogleMapsEndpoint(requested string, resolvedCandidates []string) googleMapsEndpointIdentity {
	requested = strings.TrimSpace(requested)
	requestedName := requested
	if comma := strings.Index(requestedName, ","); comma >= 0 {
		requestedName = requestedName[:comma]
	}
	requestedTokens := googleMapsIdentityTokens(requestedName)
	identity := googleMapsEndpointIdentity{
		Requested: requested, RequestedName: strings.TrimSpace(requestedName), State: "unknown", MissingTokens: []string{},
	}
	if len(resolvedCandidates) == 0 || len(requestedTokens) == 0 {
		return identity
	}
	bestResolved := ""
	bestMissing := append([]string(nil), requestedTokens...)
	for _, candidate := range resolvedCandidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		candidateSet := map[string]bool{}
		for _, token := range googleMapsIdentityTokens(candidate) {
			candidateSet[token] = true
		}
		missing := []string{}
		for _, token := range requestedTokens {
			if !candidateSet[token] {
				missing = append(missing, token)
			}
		}
		if len(missing) == 0 {
			identity.Resolved = candidate
			identity.State = "matched"
			return identity
		}
		if bestResolved == "" || len(missing) < len(bestMissing) {
			bestResolved, bestMissing = candidate, missing
		}
	}
	if bestResolved == "" {
		return identity
	}
	identity.Resolved = bestResolved
	identity.State = "mismatch"
	identity.MissingTokens = bestMissing
	return identity
}

func googleMapsIdentityTokens(value string) []string {
	value = strings.ToLower(value)
	parts := strings.Fields(googleMapsTokenSeparatorPattern.ReplaceAllString(value, " "))
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "denmark" || part == "danmark" || part == "the" || len([]rune(part)) < 2 {
			continue
		}
		if _, err := strconv.Atoi(part); err == nil {
			continue
		}
		tokens = append(tokens, part)
	}
	return tokens
}

func googleMapsIncidents(text string) []googleMapsRouteIncident {
	incidents := []googleMapsRouteIncident{}
	if match := googleMapsRoadClosurePattern.FindString(text); match != "" {
		incidents = append(incidents, googleMapsRouteIncident{Kind: "road_closure", Severity: "blocking", Text: match})
	}
	return incidents
}

func googleMapsRouteWarnings(routes []googleMapsRoute) []string {
	closureCount := 0
	for _, route := range routes {
		for _, incident := range route.Incidents {
			if incident.Kind == "road_closure" {
				closureCount++
				break
			}
		}
	}
	if closureCount == 0 {
		return []string{}
	}
	return []string{fmt.Sprintf("Google Maps marks %d route(s) with a road closure; verify access before relying on these estimates.", closureCount)}
}

func classifyGoogleMapsRouteTrust(routes []googleMapsRoute, origin, destination googleMapsEndpointIdentity) googleMapsRouteTrust {
	if len(routes) == 0 {
		return googleMapsRouteTrust{Level: "untrusted", Status: "route_not_ready", Reasons: []string{"no_complete_route_cards"}}
	}
	if origin.State == "mismatch" || destination.State == "mismatch" {
		reasons := []string{}
		if origin.State == "mismatch" {
			reasons = append(reasons, "origin_identity_mismatch")
		}
		if destination.State == "mismatch" {
			reasons = append(reasons, "destination_identity_mismatch")
		}
		return googleMapsRouteTrust{Level: "untrusted", Status: "location_mismatch", Reasons: reasons}
	}
	reasons := []string{}
	if origin.State != "matched" {
		reasons = append(reasons, "origin_identity_unverified")
	}
	if destination.State != "matched" {
		reasons = append(reasons, "destination_identity_unverified")
	}
	if len(googleMapsRouteWarnings(routes)) > 0 {
		reasons = append(reasons, "route_incident_visible")
	}
	if len(reasons) > 0 {
		return googleMapsRouteTrust{Level: "reduced", Status: "success_with_warnings", Reasons: reasons}
	}
	return googleMapsRouteTrust{Level: "trusted", Status: "success", Reasons: []string{}}
}

func parseGoogleMapsDuration(match []string) int {
	if len(match) < 4 {
		return 0
	}
	hours, minutes := 0, 0
	if match[1] != "" {
		hours, _ = strconv.Atoi(match[1])
	}
	if match[2] != "" {
		minutes, _ = strconv.Atoi(match[2])
	}
	if match[3] != "" {
		hours, _ = strconv.Atoi(match[3])
	}
	return hours*60 + minutes
}

func parseGoogleMapsDistance(match []string) float64 {
	if len(match) < 4 {
		return 0
	}
	raw, unit := match[1], strings.ToLower(match[2])
	if raw == "" {
		raw, unit = match[3], "m"
	}
	value, _ := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64)
	switch unit {
	case "m":
		return math.Round(value) / 1000
	case "mi", "mile", "miles":
		return math.Round(value*1.609344*1000) / 1000
	default:
		return value
	}
}

func truncateGoogleMapsText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func googleMapsDirectionsURL(origin, destination, travelMode string) string {
	query := url.Values{}
	query.Set("api", "1")
	query.Set("origin", origin)
	query.Set("destination", destination)
	query.Set("travelmode", travelMode)
	query.Set("hl", "en")
	return "https://www.google.com/maps/dir/?" + query.Encode()
}

func summarizeGoogleMapsRoutes(routes []googleMapsRoute) (googleMapsRoute, googleMapsRoute, googleMapsRoute) {
	selected := routes[0]
	fastest, shortest := selected, selected
	for _, route := range routes[1:] {
		if route.DurationMinutes < fastest.DurationMinutes {
			fastest = route
		}
		if route.DistanceKM < shortest.DistanceKM {
			shortest = route
		}
	}
	return selected, fastest, shortest
}

func sortGoogleMapsRoutes(routes []googleMapsRoute) []googleMapsRoute {
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].Index < routes[j].Index })
	return routes
}

const googleMapsDirectionsExpression = `(() => {
  "__cdp_cli_google_maps_directions__";
  const normalize = value => String(value || "").replace(/\s+/g, " ").trim();
  const bodyText = normalize(document.body ? (document.body.innerText || document.body.textContent || "") : "");
  const lower = bodyText.toLowerCase();
  let pageState = "unknown";
  if (lower.includes("did you mean:")) pageState = "location_ambiguous";
  else if (lower.includes("before you continue") || lower.includes("accept all") || lower.includes("reject all")) pageState = "consent_required";
  else if (lower.includes("unusual traffic") || lower.includes("captcha")) pageState = "blocked";
  const signal = /\b(\d+\s*(?:h|hr|hrs|hour|hours|min|mins|minute|minutes)|\d+(?:[.,]\d+)?\s*(?:km|mi|mile|miles)|via)\b/i;
  const cards = [];
  const seen = new Set();
  const nodes = document.querySelectorAll("[aria-label], [role='main'] div, [role='main'] span, div[role='button'], button");
  for (const node of Array.from(nodes).slice(0, 2500)) {
    const aria = normalize(node.getAttribute && node.getAttribute("aria-label"));
    const text = normalize(node.innerText || node.textContent || "");
    const raw = normalize([aria, text].filter(Boolean).join(" "));
    if (!raw || raw.length < 4 || raw.length > 900 || !signal.test(raw)) continue;
    const key = raw.slice(0, 260).toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    cards.push({text: raw.slice(0, 900), aria_label: aria.slice(0, 240), role: normalize(node.getAttribute && node.getAttribute("role"))});
    if (cards.length >= 40) break;
  }
  const inputs = Array.from(document.querySelectorAll("input")).map(input => ({
    value: normalize(input.value),
    aria: normalize(input.getAttribute("aria-label"))
  })).filter(input => input.value);
  const originInput = inputs.find(input => /^starting point\b/i.test(input.aria));
  const destinationInput = inputs.find(input => /^destination\b/i.test(input.aria));
  return {
    title: normalize(document.title).slice(0, 300),
    url: String(location.href || "").slice(0, 2000),
    visible_text_length: bodyText.length,
    page_state: pageState,
    origin_labels: originInput ? [originInput.value.slice(0, 300)] : [],
    destination_labels: destinationInput ? [destinationInput.value.slice(0, 300)] : [],
    cards
  };
})()`

func collectGoogleMapsDirections(ctx context.Context, session *cdp.PageSession, wait time.Duration, travelMode string) (googleMapsDirectionsExtraction, []googleMapsRoute, int, time.Duration, error) {
	if wait < 0 {
		return googleMapsDirectionsExtraction{}, nil, 0, 0, commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow google-maps-directions <origin> <destination> --wait 15s --json"})
	}
	started := time.Now()
	deadline := started.Add(wait)
	attempts := 0
	var last googleMapsDirectionsExtraction
	for {
		attempts++
		result, err := session.Evaluate(ctx, googleMapsDirectionsExpression, true)
		if err != nil {
			return last, nil, attempts, time.Since(started), commandError("connection_failed", "connection", fmt.Sprintf("evaluate Google Maps target %s: %v", session.TargetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"})
		}
		if result.Exception != nil {
			return last, nil, attempts, time.Since(started), commandError("javascript_exception", "runtime", result.Exception.Text, ExitCheckFailed, []string{"cdp eval 'document.title' --target " + session.TargetID + " --json"})
		}
		if err := json.Unmarshal(result.Object.Value, &last); err != nil {
			return last, nil, attempts, time.Since(started), commandError("invalid_workflow_result", "internal", fmt.Sprintf("decode Google Maps extraction: %v", err), ExitInternal, []string{"cdp doctor --json"})
		}
		routes := parseGoogleMapsRouteCardsForMode(last.Cards, travelMode)
		if len(routes) > 0 || last.PageState == "location_ambiguous" || last.PageState == "consent_required" || last.PageState == "blocked" || wait == 0 || time.Now().After(deadline) {
			return last, routes, attempts, time.Since(started), nil
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, nil, attempts, time.Since(started), commandError("timeout", "timeout", ctx.Err().Error(), ExitTimeout, []string{"cdp workflow google-maps-directions <origin> <destination> --wait 30s --json"})
		case <-timer.C:
		}
	}
}

func googleMapsTitleEndpointLabels(title string) (origin, destination []string) {
	title = strings.TrimSpace(strings.TrimSuffix(title, " - Google Maps"))
	parts := strings.SplitN(title, " to ", 2)
	if len(parts) != 2 {
		return nil, nil
	}
	return []string{strings.TrimSpace(parts[0])}, []string{strings.TrimSpace(parts[1])}
}

func (a *app) newWorkflowGoogleMapsDirectionsCommand() *cobra.Command {
	var wait time.Duration
	var travelMode string
	cmd := &cobra.Command{
		Use:   "google-maps-directions <origin> <destination>",
		Short: "Read visible Google Maps driving or public-transit route cards with trust evidence",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			origin, destination := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			if origin == "" || destination == "" {
				return commandError("usage", "usage", "origin and destination must be non-empty", ExitUsage, []string{"cdp workflow google-maps-directions <origin> <destination> --json"})
			}
			if wait < 0 {
				return commandError("usage", "usage", "--wait must be non-negative", ExitUsage, []string{"cdp workflow google-maps-directions <origin> <destination> --wait 15s --json"})
			}
			travelMode = strings.ToLower(strings.TrimSpace(travelMode))
			if travelMode != "driving" && travelMode != "transit" {
				return commandError("usage", "usage", "--travel-mode must be driving or transit", ExitUsage, []string{"cdp workflow google-maps-directions <origin> <destination> --travel-mode transit --json"})
			}
			ctx, cancel := a.commandContextWithDefault(cmd, wait+20*time.Second)
			defer cancel()
			client, closeClient, err := a.browserCDPClient(ctx)
			if err != nil {
				return commandError("connection_not_configured", "connection", err.Error(), ExitConnection, a.connectionRemediationCommands())
			}
			rawURL := googleMapsDirectionsURL(origin, destination, travelMode)
			targetID, err := a.createWorkflowPageTarget(ctx, client, rawURL, "google-maps-directions")
			if err != nil {
				_ = closeClient(ctx)
				return err
			}
			cleanupGuard := &renderedExtractCleanupGuard{client: client, targetID: targetID, owned: true}
			session, err := cdp.AttachToTargetWithClient(ctx, client, targetID, closeClient)
			if err != nil {
				cleanup := cleanupGuard.cleanup()
				_ = closeClient(ctx)
				return commandErrorWithData("connection_failed", "connection", fmt.Sprintf("attach Google Maps target %s: %v", targetID, err), ExitConnection, []string{"cdp pages --json", "cdp doctor --json"}, map[string]any{"cleanup": cleanup})
			}
			defer closeRenderedExtractSession(session, nil)
			defer cleanupGuard.cleanup()

			extraction, routes, attempts, elapsed, collectErr := collectGoogleMapsDirections(ctx, session, wait, travelMode)
			originTitle, destinationTitle := googleMapsTitleEndpointLabels(extraction.Title)
			if len(extraction.OriginLabels) == 0 {
				extraction.OriginLabels = originTitle
			}
			if len(extraction.DestinationLabels) == 0 {
				extraction.DestinationLabels = destinationTitle
			}
			originIdentity := matchGoogleMapsEndpoint(origin, extraction.OriginLabels)
			destinationIdentity := matchGoogleMapsEndpoint(destination, extraction.DestinationLabels)
			trust := classifyGoogleMapsRouteTrust(routes, originIdentity, destinationIdentity)
			warnings := googleMapsRouteWarnings(routes)
			if originIdentity.State == "unknown" || destinationIdentity.State == "unknown" {
				warnings = append(warnings, "One or more resolved endpoint labels were not visible; route identity is unverified.")
			}
			evidence := googleMapsDirectionsEvidence{TargetID: targetID, PageTitle: extraction.Title, FinalURL: extraction.URL, VisibleTextLength: extraction.VisibleTextLength, AttemptCount: attempts, ElapsedMS: elapsed.Milliseconds(), Bounded: true}
			cleanup := cleanupGuard.cleanup()
			if cleanup.Error != "" {
				return commandErrorWithData("google_maps_cleanup_failed", "internal", "Google Maps route was collected but the workflow-owned tab did not close", ExitInternal, []string{cleanup.RecoveryCommand, "cdp pages --json"}, map[string]any{"cleanup": cleanup, "evidence": evidence})
			}
			if collectErr != nil {
				return commandErrorWithData("google_maps_collection_failed", "check_failed", collectErr.Error(), exitCode(collectErr), []string{"cdp workflow google-maps-directions <origin> <destination> --wait 30s --json"}, map[string]any{"cleanup": cleanup, "evidence": evidence})
			}
			if extraction.PageState != "unknown" && len(routes) == 0 {
				return commandErrorWithData(extraction.PageState, "stop_state", "Google Maps did not expose a usable route because the visible page requires attention", ExitCheckFailed, []string{"cdp pages --json"}, map[string]any{"page_state": extraction.PageState, "cleanup": cleanup, "evidence": evidence})
			}
			payload := map[string]any{
				"query":  map[string]string{"origin": origin, "destination": destination, "travel_mode": travelMode},
				"status": trust.Status, "trust": trust, "origin_identity": originIdentity, "destination_identity": destinationIdentity,
				"routes": routes, "warnings": warnings, "evidence": evidence, "cleanup": cleanup,
			}
			if len(routes) == 0 {
				return commandErrorWithData("route_not_ready", "check_failed", "no complete visible route card appeared before the wait deadline", ExitCheckFailed, []string{"cdp workflow google-maps-directions <origin> <destination> --wait 30s --json"}, payload)
			}
			selected, fastest, shortest := summarizeGoogleMapsRoutes(sortGoogleMapsRoutes(routes))
			payload["selected_route"], payload["fastest_route"] = selected, fastest
			if travelMode == "driving" {
				payload["shortest_route"] = shortest
			}
			if trust.Level == "untrusted" {
				return commandErrorWithData(trust.Status, "check_failed", "Google Maps resolved an endpoint that does not match the requested place", ExitCheckFailed, []string{"Retry with an exact street address or coordinates."}, payload)
			}
			payload["ok"] = true
			return a.render(ctx, fmt.Sprintf("%d route(s)\t%s", len(routes), trust.Level), payload)
		},
	}
	cmd.Flags().DurationVar(&wait, "wait", 15*time.Second, "maximum time to wait for complete visible route cards")
	cmd.Flags().StringVar(&travelMode, "travel-mode", "driving", "route mode: driving or transit")
	return cmd
}
