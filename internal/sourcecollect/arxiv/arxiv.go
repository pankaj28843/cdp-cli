// Package arxiv contains pure arXiv paper identity policy.
package arxiv

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var modern = regexp.MustCompile(`^[0-9]{4}\.[0-9]{4,5}(?:v[1-9][0-9]*)?$`)
var legacy = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.-]*/[0-9]{7}(?:v[1-9][0-9]*)?$`)

type Request struct {
	URL        string `json:"url"`
	Identifier string `json:"identifier"`
}

func Parse(raw string) (Request, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "https" || (u.Hostname() != "arxiv.org" && u.Hostname() != "www.arxiv.org") || u.RawQuery != "" || u.Fragment != "" {
		return Request{}, fmt.Errorf("unsupported arXiv URL")
	}
	p := strings.TrimPrefix(u.Path, "/")
	route, id, ok := strings.Cut(p, "/")
	if !ok || (route != "abs" && route != "html" && route != "pdf") {
		return Request{}, fmt.Errorf("unsupported arXiv route")
	}
	if route == "pdf" {
		id = strings.TrimSuffix(id, ".pdf")
	}
	if !validModern(id) && !legacy.MatchString(id) {
		return Request{}, fmt.Errorf("unsupported arXiv identifier")
	}
	return Request{URL: raw, Identifier: id}, nil
}

func validModern(id string) bool {
	if !modern.MatchString(id) {
		return false
	}
	base := strings.Split(id, "v")[0]
	year, _ := strconv.Atoi(base[:2])
	month, _ := strconv.Atoi(base[2:4])
	seq := strings.Split(base, ".")[1]
	return year >= 7 && month >= 1 && month <= 12 && ((year < 15 && len(seq) == 4) || (year >= 15 && len(seq) == 5))
}

func ValidateFinalURL(request Request, finalURL string) (Request, error) {
	final, err := Parse(finalURL)
	if err != nil {
		return Request{}, fmt.Errorf("invalid final arXiv URL: %w", err)
	}
	requestedBase, requestedVersion := splitVersion(request.Identifier)
	finalBase, finalVersion := splitVersion(final.Identifier)
	if requestedBase != finalBase || (requestedVersion != "" && requestedVersion != finalVersion) || (requestedVersion == "" && finalVersion == "") {
		return Request{}, fmt.Errorf("arXiv identity changed or version was not resolved")
	}
	return final, nil
}
func splitVersion(id string) (string, string) {
	i := strings.LastIndex(id, "v")
	if i > 0 && i+1 < len(id) {
		all := true
		for _, r := range id[i+1:] {
			if r < '0' || r > '9' {
				all = false
			}
		}
		if all {
			return id[:i], id[i:]
		}
	}
	return id, ""
}
