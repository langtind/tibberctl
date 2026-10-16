package cli

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Output is Markdown, which reads well both in a terminal and when an agent
// relays it into a chat. What the service returned is printed on one line,
// so that a line break in a name cannot pass for structure.

func renderStatus(w io.Writer, status loginStatus) {
	switch {
	case !status.LoggedIn:
		fmt.Fprintf(w, "Not logged in. Run \"%s login\".\n", Name)
	case status.Source == "environment":
		fmt.Fprintf(w, "Logged in with the token in %s.\n", envToken)
	default:
		fmt.Fprintln(w, "Logged in. The token is in the system keyring and does not run out.")
	}
}

func renderHomes(w io.Writer, list homeList) {
	fmt.Fprintln(w, "# Homes")
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo homes.")
		return
	}
	fmt.Fprintln(w)
	for _, h := range list.Items {
		fmt.Fprintf(w, "- %s\n", oneLine(h.Name()))
		if h.Nickname != "" {
			field(w, "Address", strings.TrimSpace(h.Address+", "+strings.TrimSpace(h.Postcode+" "+h.City)))
		}
		field(w, "Price area", h.PriceArea)
		field(w, "Grid company", h.GridCompany)
		field(w, "Agreement", h.Subscription)
		if h.RealTime {
			field(w, "Live meter", "yes")
		}
		field(w, "id", h.ID)
	}
}

func renderUsage(w io.Writer, r usageReport) {
	days := r.From
	if r.To != r.From {
		days += " to " + r.To
	}
	fmt.Fprintf(w, "# %s, %s\n\n", oneLine(r.Home), days)
	if r.Periods == 0 {
		fmt.Fprintln(w, "No readings in this period.")
		if r.Missing > 0 {
			fmt.Fprintf(w, "%d %s periods have no reading yet.\n", r.Missing, r.Resolution)
		}
		return
	}
	fmt.Fprintf(w, "Consumption: %s kWh\n", kwh(r.Consumption))
	fmt.Fprintf(w, "Cost: %s %s\n", money(r.Cost), r.Currency)
	if r.AveragePrice != nil {
		fmt.Fprintf(w, "Average price: %s %s/kWh\n", price(*r.AveragePrice), r.Currency)
	}
	if r.Peak != nil {
		fmt.Fprintf(w, "Peak: %s kWh, %s\n", kwh(*r.Peak.Consumption), span(r.Peak, r.Resolution))
	}
	if r.Dearest != nil && r.Dearest.Cost != nil {
		fmt.Fprintf(w, "Dearest: %s %s, %s\n", money(*r.Dearest.Cost), r.Currency, span(r.Dearest, r.Resolution))
	}
	if r.Missing > 0 {
		fmt.Fprintf(w, "Missing: %d %s periods have no reading, and are not in the totals.\n", r.Missing, r.Resolution)
	}
	fmt.Fprintln(w, "Cost is the energy from Tibber with VAT, without grid rent.")

	order := "by time"
	if r.Sort != "time" {
		order = "highest " + r.Sort + " first"
	}
	fmt.Fprintf(w, "\n## Periods, %s\n\n", order)
	fmt.Fprintf(w, "| %s | kWh | Cost | Price/kWh |\n| --- | ---: | ---: | ---: |\n", periodHeading(r.Resolution))
	for i := range r.Items {
		p := &r.Items[i]
		fmt.Fprintf(w, "| %s | %s | %s | %s |\n", span(p, r.Resolution), optional(p.Consumption, kwh), optional(p.Cost, money), optional(p.UnitPrice, price))
	}
	if r.Truncated {
		fmt.Fprintln(w, "\nThere are more periods. Raise --limit to see them. The totals above cover all of them.")
	}
}

func renderPrices(w io.Writer, r priceReport) {
	fmt.Fprintf(w, "# Prices, %s\n\n", oneLine(r.Home))
	if r.Current != nil && r.Current.Total != nil {
		fmt.Fprintf(w, "Now: %s %s/kWh (%s)\n", price(*r.Current.Total), r.Current.Currency, level(r.Current.Level))
	}
	for _, day := range []struct {
		name string
		s    *daySummary
	}{{"Today", r.Today}, {"Tomorrow", r.Tomorrow}} {
		if day.s == nil {
			fmt.Fprintf(w, "%s: not out yet\n", day.name)
			continue
		}
		fmt.Fprintf(w, "%s: average %s, cheapest %s at %s, dearest %s at %s %s/kWh\n", day.name,
			price(day.s.Average), price(*day.s.Cheapest.Total), clock(day.s.Cheapest.StartsAt),
			price(*day.s.Dearest.Total), clock(day.s.Dearest.StartsAt), day.s.Currency)
	}
	if len(r.Items) == 0 {
		return
	}
	fmt.Fprintln(w, "\n| Starts | Price/kWh | Level |\n| --- | ---: | --- |")
	for _, p := range r.Items {
		fmt.Fprintf(w, "| %s | %s | %s |\n", stamp(p.StartsAt), optional(p.Total, price), level(p.Level))
	}
}

func periodHeading(resolution string) string {
	switch resolution {
	case "hourly":
		return "Hour"
	case "daily":
		return "Day"
	case "weekly":
		return "Week"
	case "monthly":
		return "Month"
	}
	return "Year"
}

// span writes a period the way its resolution reads: an hour as the day and
// the hours, a day as its date, a month as the month.
func span(p *period, resolution string) string {
	from, err1 := time.Parse(time.RFC3339, p.From)
	to, err2 := time.Parse(time.RFC3339, p.To)
	if err1 != nil || err2 != nil {
		return p.From
	}
	switch resolution {
	case "hourly":
		return from.Format("2006-01-02 15:04") + "-" + to.Format("15:04")
	case "daily":
		return from.Format("2006-01-02 Mon")
	case "weekly":
		return from.Format("2006-01-02") + " to " + to.AddDate(0, 0, -1).Format("2006-01-02")
	case "monthly":
		return from.Format("2006-01")
	}
	return from.Format("2006")
}

func clock(startsAt string) string {
	if t, err := time.Parse(time.RFC3339, startsAt); err == nil {
		return t.Format("15:04")
	}
	return startsAt
}

func stamp(startsAt string) string {
	if t, err := time.Parse(time.RFC3339, startsAt); err == nil {
		return t.Format("2006-01-02 15:04")
	}
	return startsAt
}

func level(l string) string {
	return strings.ReplaceAll(strings.ToLower(l), "_", " ")
}

func kwh(v float64) string   { return fmt.Sprintf("%.2f", v) }
func money(v float64) string { return fmt.Sprintf("%.2f", v) }
func price(v float64) string { return fmt.Sprintf("%.4f", v) }

func optional(v *float64, format func(float64) string) string {
	if v == nil {
		return "-"
	}
	return format(*v)
}

// field writes one indented line under a list item, and nothing for an
// empty value.
func field(w io.Writer, label, value string) {
	if value != "" {
		fmt.Fprintf(w, "  %s: %s\n", label, oneLine(value))
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
