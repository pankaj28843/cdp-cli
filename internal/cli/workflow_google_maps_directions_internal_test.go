package cli

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestGoogleMapsDirectionsParseRouteCardsDeduplicatesAndRejectsIncomplete(t *testing.T) {
	cards := []googleMapsRouteCard{
		{Text: "Google Maps Best 13 min 7.7 km via Søndersognsvej and Rødkildevej Road closure Details Preview 14 min 10.3 km via Route 287 Road closure Send directions Copy link", Role: "main"},
		{Text: "13 min 7.7 km via Søndersognsvej and Rødkildevej Road closure", Role: "button"},
		{Text: "13 min 7.7 km"},
		{Text: "14 min 10.3 km via Søndersognsvej and Route 287 Road closure"},
		{Text: "14 min via missing distance"},
	}

	routes := parseGoogleMapsRouteCards(cards)
	if len(routes) != 2 {
		t.Fatalf("routes = %+v, want two complete deduplicated routes", routes)
	}
	if routes[0].DurationMinutes != 13 || routes[0].DistanceKM != 7.7 || routes[0].Name != "Søndersognsvej and Rødkildevej" {
		t.Fatalf("first route = %+v", routes[0])
	}
	if routes[0].Summary != "13 min 7.7 km via Søndersognsvej and Rødkildevej Road closure" {
		t.Fatalf("first route summary = %q, want focused card rather than enclosing page text", routes[0].Summary)
	}
	if routes[1].DurationMinutes != 14 || routes[1].DistanceKM != 10.3 {
		t.Fatalf("second route = %+v", routes[1])
	}
}

func TestGoogleMapsDirectionsParseRouteCardsDoesNotTreatCompactHoursAsMetres(t *testing.T) {
	routes := parseGoogleMapsRouteCards([]googleMapsRouteCard{{Text: "1h 23m via E47"}})
	if len(routes) != 0 {
		t.Fatalf("routes = %+v, compact duration fragment must not become a metre distance", routes)
	}
}

func TestGoogleMapsDirectionsParsesPublicTransitWithoutDistance(t *testing.T) {
	routes := parseGoogleMapsRouteCardsForMode([]googleMapsRouteCard{
		{Text: "4 hr 8 min 9:35 AM—1:43 PM (Tuesday) Flybussen 200 then train 91", Role: "button"},
		{Text: "4 hr 8 min 9:35 AM—1:43 PM (Tuesday) Flybussen 200 then train 91"},
		{Text: "5 hr 2 min 10:15 AM—3:17 PM Bus and train", Role: "button"},
	}, "transit")
	if len(routes) != 2 {
		t.Fatalf("routes = %+v, want two complete deduplicated transit routes", routes)
	}
	if routes[0].DurationMinutes != 248 || routes[0].DistanceKM != 0 || routes[0].DepartureTime != "9:35 AM" || routes[0].ArrivalTime != "1:43 PM" || routes[0].ArrivalDay != "Tuesday" {
		t.Fatalf("first transit route = %+v", routes[0])
	}
	withDepartureDay := parseGoogleMapsRouteCardsForMode([]googleMapsRouteCard{{Text: "3 hr 40 min 1:01 PM (Wednesday)—4:41 PM Bus and train", Role: "button"}}, "transit")
	if len(withDepartureDay) != 1 || withDepartureDay[0].DepartureDay != "Wednesday" || withDepartureDay[0].ArrivalTime != "4:41 PM" {
		t.Fatalf("departure-day transit route = %+v", withDepartureDay)
	}
}

func TestGoogleMapsDirectionsURLSelectsTravelMode(t *testing.T) {
	raw := googleMapsDirectionsURL("Harstad/Narvik Airport, Evenes", "Abisko Östra", "transit")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if got := parsed.Query().Get("travelmode"); got != "transit" {
		t.Fatalf("travelmode = %q, want transit", got)
	}
}

func TestGoogleMapsDirectionsPromotesRoadClosureIncidents(t *testing.T) {
	routes := parseGoogleMapsRouteCards([]googleMapsRouteCard{
		{Text: "13 min 7.7 km via Søndersognsvej and Rødkildevej Road closure Details Preview"},
		{Text: "14 min 10.3 km via Route 287 Road closure"},
	})
	warnings := googleMapsRouteWarnings(routes)
	if len(routes) != 2 || len(routes[0].Incidents) != 1 || routes[0].Incidents[0].Kind != "road_closure" {
		t.Fatalf("routes = %+v, want structured road closure", routes)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "2 route") {
		t.Fatalf("warnings = %+v, want promoted closure summary", warnings)
	}
}

func TestGoogleMapsDirectionsCleanRouteHasNoIncidentWarning(t *testing.T) {
	routes := parseGoogleMapsRouteCards([]googleMapsRouteCard{{Text: "7 min 5.1 km via Søndersognsvej Fastest route"}})
	if len(routes) != 1 || len(routes[0].Incidents) != 0 || len(googleMapsRouteWarnings(routes)) != 0 {
		t.Fatalf("routes=%+v warnings=%+v, want clean route", routes, googleMapsRouteWarnings(routes))
	}
}

func TestGoogleMapsDirectionsDetectsConfidentDestinationMismatch(t *testing.T) {
	identity := matchGoogleMapsEndpoint("Møn Is, Denmark", []string{"Møn, Vordingborg Municipality"})
	if identity.State != "mismatch" || len(identity.MissingTokens) == 0 || identity.MissingTokens[0] != "is" {
		t.Fatalf("identity = %+v, want missing Møn Is identity token", identity)
	}
}

func TestGoogleMapsDirectionsAcceptsExactBusinessResolution(t *testing.T) {
	identity := matchGoogleMapsEndpoint(
		"Møn Is, Hovgårdsvej 4, 4780 Stege, Denmark",
		[]string{"Møn Is, Hovgårdsvej 4, 4780 Stege"},
	)
	if identity.State != "matched" || len(identity.MissingTokens) != 0 {
		t.Fatalf("identity = %+v, want exact business match", identity)
	}
}

func TestGoogleMapsDirectionsKeepsIdentityUnknownWithoutVisibleResolution(t *testing.T) {
	identity := matchGoogleMapsEndpoint("Møn Is, Denmark", nil)
	if identity.State != "unknown" {
		t.Fatalf("identity = %+v, want unknown rather than guessed mismatch", identity)
	}
}

func TestGoogleMapsDirectionsTrustClassification(t *testing.T) {
	clean := parseGoogleMapsRouteCards([]googleMapsRouteCard{{Text: "7 min 5.1 km via Søndersognsvej Fastest route"}})
	closed := parseGoogleMapsRouteCards([]googleMapsRouteCard{{Text: "13 min 7.7 km via Rødkildevej Road closure"}})
	matched := googleMapsEndpointIdentity{State: "matched"}
	unknown := googleMapsEndpointIdentity{State: "unknown"}
	mismatch := googleMapsEndpointIdentity{State: "mismatch"}
	tests := []struct {
		name   string
		routes []googleMapsRoute
		origin googleMapsEndpointIdentity
		dest   googleMapsEndpointIdentity
		level  string
		status string
	}{
		{name: "trusted", routes: clean, origin: matched, dest: matched, level: "trusted", status: "success"},
		{name: "closure", routes: closed, origin: matched, dest: matched, level: "reduced", status: "success_with_warnings"},
		{name: "identity unknown", routes: clean, origin: matched, dest: unknown, level: "reduced", status: "success_with_warnings"},
		{name: "identity mismatch", routes: clean, origin: matched, dest: mismatch, level: "untrusted", status: "location_mismatch"},
		{name: "no routes", routes: nil, origin: matched, dest: matched, level: "untrusted", status: "route_not_ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trust := classifyGoogleMapsRouteTrust(tt.routes, tt.origin, tt.dest)
			if trust.Level != tt.level || trust.Status != tt.status || len(trust.Reasons) == 0 && trust.Level != "trusted" {
				t.Fatalf("trust = %+v, want level=%s status=%s", trust, tt.level, tt.status)
			}
		})
	}
}

func TestGoogleMapsDirectionsCommandIsDiscoverable(t *testing.T) {
	root := (&app{out: io.Discard, err: io.Discard}).newRoot()
	command, _, err := root.Find([]string{"workflow", "google-maps-directions"})
	if err != nil || command == nil {
		t.Fatalf("workflow google-maps-directions not found: %v", err)
	}
	if !strings.Contains(command.Use, "<origin>") || !strings.Contains(command.Use, "<destination>") {
		t.Fatalf("command use = %q, want explicit origin and destination", command.Use)
	}
}
