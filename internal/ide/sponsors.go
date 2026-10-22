// Copyright (C) 2017-2026 The Rune Authors
// SPDX-License-Identifier: GPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or (at
// your option) any later version.
//
// This program is distributed in the hope that it will be useful, but
// WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
// General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package ide

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/template"
	"time"
)

//go:embed sponsors.md.tmpl
var sponsorsTmplText string

var sponsorsTmpl = template.Must(template.New("sponsors").Funcs(template.FuncMap{
	"link": sponsorLink,
}).Parse(sponsorsTmplText))

const (
	sponsorsWidth = 80
	// sponsorsFetchTimeout bounds how long :sponsors waits on the API
	// before showing the fallback, since nothing is on screen meanwhile.
	sponsorsFetchTimeout = 5 * time.Second
	// defaultSponsorURL is linked when the sponsor list, which carries
	// the authoritative link, is unavailable.
	defaultSponsorURL = "https://github.com/sponsors/unstablebuild"
	sponsorsMaxBytes  = 1 << 20
)

// sponsorsResponse mirrors the JSON served by the API's /api/sponsors.
type sponsorsResponse struct {
	SponsorURL   string        `json:"sponsor_url"`
	Tiers        []sponsorTier `json:"tiers"`
	PrivateCount int           `json:"private_count"`
}

type sponsorTier struct {
	ID       string    `json:"id"`
	Sponsors []sponsor `json:"sponsors"`
}

type sponsor struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}

// sponsorRows are the rows of the website's sponsors page, highest
// first, each listing its tiers' sponsors in the order the ids appear.
// Sponsors in any other tier are not shown.
var sponsorRows = []struct {
	label string
	tiers []string
	// openSlot rows are shown even when empty, ending in an invitation
	// to take the next spot.
	openSlot bool
	// githubTier is GitHub's numeric id for the one tier the row's open
	// slot leads to, matching the tier links on the page. Rows spanning
	// several tiers leave it 0 and link to the sponsor page instead.
	githubTier int
}{
	{"Diamond Sponsors", []string{"diamond"}, true, 671970},
	{"Platinum Sponsors", []string{"platinum"}, true, 671967},
	{"Other Sponsors", []string{"gold", "silver", "bronze"}, true, 0},
	{"Backers", []string{"backer"}, false, 0},
}

type sponsorsPage struct {
	SponsorURL   string
	Rows         []sponsorRow
	PrivateCount int
}

type sponsorRow struct {
	Label    string
	Sponsors []sponsor
	// SlotURL is where the row's open slot links, or empty if the row
	// has none.
	SlotURL string
}

// renderSponsors lays out the sponsors page as the website does. The
// zero response renders every row with only its open slot, which is
// what is shown when the sponsor list cannot be fetched.
func renderSponsors(resp sponsorsResponse) (string, error) {
	byTier := make(map[string][]sponsor, len(resp.Tiers))
	for _, t := range resp.Tiers {
		byTier[t.ID] = append(byTier[t.ID], t.Sponsors...)
	}
	page := sponsorsPage{
		SponsorURL:   cmp.Or(resp.SponsorURL, defaultSponsorURL),
		PrivateCount: resp.PrivateCount,
	}
	for _, r := range sponsorRows {
		row := sponsorRow{Label: r.label}
		for _, id := range r.tiers {
			row.Sponsors = append(row.Sponsors, byTier[id]...)
		}
		switch {
		case r.openSlot && r.githubTier != 0:
			row.SlotURL = fmt.Sprintf("%s/sponsorships?tier_id=%d", page.SponsorURL, r.githubTier)
		case r.openSlot:
			row.SlotURL = page.SponsorURL
		}
		if row.SlotURL != "" || len(row.Sponsors) > 0 {
			page.Rows = append(page.Rows, row)
		}
	}
	var b strings.Builder
	if err := sponsorsTmpl.Execute(&b, page); err != nil {
		return "", fmt.Errorf("render sponsors: %w", err)
	}
	return b.String(), nil
}

// sponsorLink links a sponsor's name only to an http(s) URL that fits in
// a <...> link destination: anything else would break out of the markup,
// or be opened as a workspace link when clicked.
func sponsorLink(s sponsor) string {
	name := escapeMarkdown(cmp.Or(s.Name, s.Login))
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		strings.ContainsAny(s.URL, "<> \t\r\n") {
		return name
	}
	return "[" + name + "](<" + s.URL + ">)"
}

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`,
	`<`, `\<`, `>`, `\>`, `#`, `\#`, `|`, `\|`,
)

// escapeMarkdown keeps names and logins chosen on GitHub from being
// read as markup.
func escapeMarkdown(s string) string {
	return markdownEscaper.Replace(s)
}

func fetchSponsors(ctx context.Context, client *http.Client, endpoint string) (sponsorsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, sponsorsFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return sponsorsResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return sponsorsResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return sponsorsResponse{}, fmt.Errorf("GET %s: %s", endpoint, resp.Status)
	}
	var ret sponsorsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, sponsorsMaxBytes)).Decode(&ret); err != nil {
		return sponsorsResponse{}, fmt.Errorf("decode sponsors: %w", err)
	}
	return ret, nil
}
