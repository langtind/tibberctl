package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/tibberctl/internal/tibber"
)

const (
	defaultRows = 100
	// maxRows is the most periods usage prints: 92 days of hours.
	maxRows = 92 * 24
)

var homeFlag = ctlkit.Flag{Name: "home", Kind: ctlkit.String, Value: "home", Help: "the home, by id, nickname, address or city. Needed when the account has more than one"}

// commands is the command table, the single description of the command
// line. Add a command or a flag here and nowhere else.
func (a *App) commands() []ctlkit.Command {
	return []ctlkit.Command{
		{
			Name:    "login",
			Summary: "Check a personal access token and store it in the system keyring",
			Long:    loginSteps,
			Flags: []ctlkit.Flag{
				{Name: "token-stdin", Kind: ctlkit.Bool, Help: "read the token from stdin instead of prompting"},
			},
			Run: a.login,
		},
		{
			Name:    "logout",
			Summary: "Remove the stored token",
			Run:     a.logout,
		},
		{
			Name:    "status",
			Summary: "Show whether a token is stored, without calling Tibber",
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag},
			Run:     a.status,
		},
		{
			Name:    "homes",
			Summary: "List the homes on the account",
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.homes,
		},
		{
			Name:    "usage",
			Summary: "Show the consumption and its cost in a period, with totals and the peak",
			Long:    usageHelp,
			Flags: []ctlkit.Flag{
				homeFlag,
				{Name: "from", Kind: ctlkit.String, Value: "date", Default: "yesterday", Help: "the first day, as yyyy-mm-dd, today or yesterday"},
				{Name: "to", Kind: ctlkit.String, Value: "date", Help: "the last day, included. Default the same as --from"},
				{Name: "resolution", Kind: ctlkit.String, Value: "period", Default: "hourly", Enum: []string{"hourly", "daily", "weekly", "monthly", "annual"}, Help: "the length of one period"},
				{Name: "sort", Kind: ctlkit.String, Value: "by", Default: "time", Enum: []string{"time", "consumption", "cost", "price"}, Help: "the order of the periods. Anything but time puts the highest first"},
				ctlkit.LimitFlag(defaultRows, maxRows),
				ctlkit.JSONFlag,
				ctlkit.FieldsFlag,
			},
			Run: a.usage,
		},
		{
			Name:    "prices",
			Summary: "Show today's and tomorrow's prices",
			Long:    "Tomorrow's prices come out in the early afternoon. Before then tomorrow is empty. Prices are per kWh with VAT, and do not include grid rent.",
			Flags: []ctlkit.Flag{
				homeFlag,
				{Name: "resolution", Kind: ctlkit.String, Value: "period", Default: "hourly", Enum: []string{"hourly", "quarter-hourly"}, Help: "the length of one period"},
				ctlkit.JSONFlag,
				ctlkit.FieldsFlag,
			},
			Run: a.prices,
		},
	}
}

const loginSteps = `Make a personal access token at https://developer.tibber.com/settings/access-token, logged in with the Tibber account. Then "` + Name + ` login" asks for it. In a script, pipe it in with --token-stdin.

The token is checked against Tibber, and then kept in the system keyring. It does not run out. Tibber revokes it when a new one is made on that page.`

const usageHelp = `The days are days in the home's time zone, and --to is included: --from 2026-09-22 --to 2026-09-25 is four days.

The totals are the sum of the periods. Cost is what Tibber charges for the energy, with VAT. Grid rent from the grid company, the monthly fee and any state support are not in it.

The peak is the period with the highest consumption. A period with no reading yet, such as the hours still to come today, counts as missing.

With --sort consumption --limit 3 the output is the three periods with the highest consumption.`

func (a *App) login(ctx context.Context, c *ctlkit.Call) error {
	token, err := c.Secret("Token", "token-stdin")
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return ctlkit.Usagef("the token is empty")
	}

	a.Client.Token = token
	homes, err := a.Client.Homes(ctx)
	if err != nil {
		if ctlkit.KindOf(err) == ctlkit.KindAuth {
			return ctlkit.Authf("Tibber refused the token: %w", err).WithHint("make a new one at https://developer.tibber.com/settings/access-token.")
		}
		return err
	}
	if err := a.Session.Save(session.Session{AccessToken: token}); err != nil {
		return fmt.Errorf("store the token in the keyring: %w", err)
	}
	if a.Getenv(envToken) != "" {
		c.Note("%s is set and takes precedence over the stored token.", envToken)
	}
	fmt.Fprintf(c.Stdout, "Logged in. The token is in the system keyring. The account has %d %s.\n", len(homes), plural(len(homes), "home", "homes"))
	return nil
}

func (a *App) logout(ctx context.Context, c *ctlkit.Call) error {
	if err := a.Session.Clear(); err != nil {
		return fmt.Errorf("remove the token from the keyring: %w", err)
	}
	if a.Getenv(envToken) != "" {
		c.Note("%s is still set and keeps working.", envToken)
	}
	fmt.Fprintln(c.Stdout, "Removed the stored token.")
	return nil
}

// loginStatus is what status prints.
type loginStatus struct {
	LoggedIn bool `json:"loggedIn"`
	// Source is "keyring", or "environment" when the variable is set.
	Source string `json:"source,omitempty"`
}

func (a *App) status(ctx context.Context, c *ctlkit.Call) error {
	status := loginStatus{}
	if strings.TrimSpace(a.Getenv(envToken)) != "" {
		status = loginStatus{LoggedIn: true, Source: "environment"}
	} else if _, err := a.Session.Load(); err == nil {
		status = loginStatus{LoggedIn: true, Source: "keyring"}
	} else if !errors.Is(err, session.ErrNotLoggedIn) {
		return err
	}
	return c.Print(status, func(w io.Writer) { renderStatus(w, status) })
}

// homeList is what homes prints.
type homeList struct {
	Items []tibber.Home `json:"items"`
}

func (a *App) homes(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	homes, err := a.Client.Homes(ctx)
	if err != nil {
		return err
	}
	list := homeList{Items: homes}
	return c.Print(list, func(w io.Writer) { renderHomes(w, list) })
}

// pickHome finds the home --home names. Without --home, an account with one
// home has that one.
func (a *App) pickHome(ctx context.Context, c *ctlkit.Call) (tibber.Home, *time.Location, error) {
	homes, err := a.Client.Homes(ctx)
	if err != nil {
		return tibber.Home{}, nil, err
	}
	home, err := matchHome(homes, c.String("home"))
	if err != nil {
		return tibber.Home{}, nil, err
	}
	zone, err := time.LoadLocation(home.TimeZone)
	if err != nil {
		zone = a.Zone
	}
	return home, zone, nil
}

func matchHome(homes []tibber.Home, want string) (tibber.Home, error) {
	names := make([]string, len(homes))
	for i, h := range homes {
		names[i] = fmt.Sprintf("%q", h.Name())
	}
	known := strings.Join(names, ", ")
	want = strings.TrimSpace(want)

	switch {
	case len(homes) == 0:
		return tibber.Home{}, ctlkit.NotFoundf("the account has no homes")
	case want == "" && len(homes) == 1:
		return homes[0], nil
	case want == "":
		return tibber.Home{}, ctlkit.Usagef("the account has %d homes, so --home is needed: %s", len(homes), known)
	}

	for _, h := range homes {
		if h.ID == want {
			return h, nil
		}
	}
	// A whole name beats a part of one, so that "Hytta" is not also "Hytta 2".
	var whole, part []tibber.Home
	needle := fold(want)
	for _, h := range homes {
		texts := []string{h.Nickname, h.Address, h.City}
		if slices.ContainsFunc(texts, func(s string) bool { return s != "" && fold(s) == needle }) {
			whole = append(whole, h)
		} else if slices.ContainsFunc(texts, func(s string) bool { return s != "" && strings.Contains(fold(s), needle) }) {
			part = append(part, h)
		}
	}
	for _, found := range [][]tibber.Home{whole, part} {
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		default:
			return tibber.Home{}, ctlkit.Usagef("--home %q fits more than one home. Use the id, as \"%s homes\" prints it", want, Name)
		}
	}
	return tibber.Home{}, ctlkit.NotFoundf("no home fits --home %q", want).WithHint("the homes are %s.", known)
}

// fold makes names compare without case and without the marks on letters,
// so that "metbacken" finds "Metbäcken" and "molle" finds "Mølle".
func fold(s string) string {
	return letters.Replace(strings.ToLower(strings.TrimSpace(s)))
}

var letters = strings.NewReplacer("å", "a", "ä", "a", "á", "a", "à", "a", "æ", "ae", "ø", "o", "ö", "o", "ó", "o", "é", "e", "è", "e", "ü", "u", "aa", "a")

// period is one row of usage, with times in the home's time zone.
type period struct {
	From        string   `json:"from"`
	To          string   `json:"to"`
	Consumption *float64 `json:"consumption"`
	Cost        *float64 `json:"cost"`
	UnitPrice   *float64 `json:"unitPrice"`
}

// usageReport is what usage prints. The totals cover every period in the
// range, also those that --limit leaves out of Items.
type usageReport struct {
	Home       string `json:"home"`
	HomeID     string `json:"homeId"`
	From       string `json:"from"`
	To         string `json:"to"`
	Resolution string `json:"resolution"`
	TimeZone   string `json:"timeZone"`
	// Consumption is in kWh, and Cost in Currency.
	Consumption float64 `json:"consumption"`
	Cost        float64 `json:"cost"`
	Currency    string  `json:"currency,omitempty"`
	// AveragePrice is Cost over Consumption: what a kWh cost on average.
	AveragePrice *float64 `json:"averagePrice"`
	Periods      int      `json:"periods"`
	// Missing is how many periods in the range have no reading.
	Missing int `json:"missing"`
	// Peak is the period with the highest consumption.
	Peak *period `json:"peak"`
	// Dearest is the period that cost the most.
	Dearest   *period  `json:"dearest"`
	Sort      string   `json:"sort"`
	Items     []period `json:"items"`
	Truncated bool     `json:"truncated"`
}

func (a *App) usage(ctx context.Context, c *ctlkit.Call) error {
	limit := c.Int("limit")
	if limit < 1 || limit > maxRows {
		return ctlkit.Usagef("--limit takes 1 to %d, not %d", maxRows, limit)
	}
	resolution := c.String("resolution")
	if err := a.authorize(ctx); err != nil {
		return err
	}
	home, zone, err := a.pickHome(ctx, c)
	if err != nil {
		return err
	}

	first, err := a.day("from", c.String("from"), zone)
	if err != nil {
		return err
	}
	last := first
	if c.Given("to") {
		if last, err = a.day("to", c.String("to"), zone); err != nil {
			return err
		}
	}
	if last.Before(first) {
		return ctlkit.Usagef("--to %s is before --from %s", last.Format(ctlkit.DateLayout), first.Format(ctlkit.DateLayout))
	}
	start, end := first, last.AddDate(0, 0, 1)
	expected := count(resolution, start, end)
	if expected > maxRows {
		return ctlkit.Usagef("%s from %s to %s is %d periods, and usage takes at most %d", resolution, first.Format(ctlkit.DateLayout), last.Format(ctlkit.DateLayout), expected, maxRows).
			WithHint("use a shorter range, or --resolution daily.")
	}

	// The cursor is a time, and which node comes right after it is not
	// something to rely on. Ask from a little before the start, and keep the
	// periods that start inside the range.
	lead := leadIn(resolution, start)
	nodes, err := a.Client.Consumption(ctx, home.ID, strings.ToUpper(resolution), tibber.Cursor(lead), count(resolution, lead, end)+1)
	if err != nil {
		return err
	}

	report := summarize(nodes, start, end, zone)
	report.Home, report.HomeID, report.TimeZone = home.Name(), home.ID, zone.String()
	report.From, report.To = first.Format(ctlkit.DateLayout), last.Format(ctlkit.DateLayout)
	report.Resolution, report.Sort = resolution, c.String("sort")
	if resolution == "hourly" || resolution == "daily" {
		report.Missing = max(expected-report.Periods, 0)
	}
	sortPeriods(report.Items, report.Sort)
	if len(report.Items) > limit {
		report.Items, report.Truncated = report.Items[:limit], true
	}
	return c.Print(report, func(w io.Writer) { renderUsage(w, report) })
}

// day reads a date flag. It takes today and yesterday as well, in zone.
func (a *App) day(flag, value string, zone *time.Location) (time.Time, error) {
	now := a.Now().In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "today":
		return today, nil
	case "yesterday":
		return today.AddDate(0, 0, -1), nil
	}
	return ctlkit.ParseDate(flag, value, zone)
}

// step moves t one period on.
func step(resolution string, t time.Time) time.Time {
	switch resolution {
	case "daily":
		return t.AddDate(0, 0, 1)
	case "weekly":
		return t.AddDate(0, 0, 7)
	case "monthly":
		return t.AddDate(0, 1, 0)
	case "annual":
		return t.AddDate(1, 0, 0)
	}
	return t.Add(time.Hour)
}

// count is how many periods start in [start, end). A period that started
// before start, such as the week that holds it, counts too.
func count(resolution string, start, end time.Time) int {
	n := 0
	for t := start; t.Before(end); t = step(resolution, t) {
		n++
	}
	if resolution != "hourly" && resolution != "daily" {
		n++
	}
	return n
}

// leadIn is where to ask from: a period or more before start.
func leadIn(resolution string, start time.Time) time.Time {
	switch resolution {
	case "daily":
		return start.AddDate(0, 0, -2)
	case "weekly":
		return start.AddDate(0, 0, -14)
	case "monthly":
		return start.AddDate(0, -2, 0)
	case "annual":
		return start.AddDate(-2, 0, 0)
	}
	return start.Add(-2 * time.Hour)
}

// summarize keeps the nodes that start in [start, end) and adds them up. A
// week, a month or a year counts when it ends after start, so that the week
// that holds start is in.
func summarize(nodes []tibber.Usage, start, end time.Time, zone *time.Location) usageReport {
	report := usageReport{Items: []period{}}
	seen := map[string]bool{}
	for _, n := range nodes {
		from, to := ctlkit.LocalTime(n.From, zone), ctlkit.LocalTime(n.To, zone)
		if from.IsZero() || !from.Before(end) || !to.After(start) || seen[n.From] {
			continue
		}
		if to.Sub(from) <= 25*time.Hour && from.Before(start) {
			continue
		}
		seen[n.From] = true
		p := period{From: from.In(zone).Format(time.RFC3339), To: to.In(zone).Format(time.RFC3339), Consumption: n.Consumption, Cost: n.Cost, UnitPrice: n.UnitPrice}
		report.Items = append(report.Items, p)
		if n.Consumption == nil {
			continue
		}
		report.Periods++
		report.Consumption += *n.Consumption
		if n.Cost != nil {
			report.Cost += *n.Cost
		}
		if n.Currency != "" {
			report.Currency = n.Currency
		}
		if report.Peak == nil || *n.Consumption > *report.Peak.Consumption {
			report.Peak = &p
		}
		if n.Cost != nil && (report.Dearest == nil || *n.Cost > *report.Dearest.Cost) {
			report.Dearest = &p
		}
	}
	if report.Consumption > 0 {
		average := report.Cost / report.Consumption
		report.AveragePrice = &average
	}
	return report
}

// sortPeriods orders the periods by time, or with the highest value first.
// A period with no value comes last.
func sortPeriods(items []period, by string) {
	value := func(p period) *float64 {
		switch by {
		case "consumption":
			return p.Consumption
		case "cost":
			return p.Cost
		case "price":
			return p.UnitPrice
		}
		return nil
	}
	if by == "time" {
		return
	}
	slices.SortStableFunc(items, func(x, y period) int {
		vx, vy := value(x), value(y)
		switch {
		case vx == nil && vy == nil:
			return 0
		case vx == nil:
			return 1
		case vy == nil:
			return -1
		}
		return cmp.Compare(*vy, *vx)
	})
}

// priceReport is what prices prints.
type priceReport struct {
	Home       string        `json:"home"`
	HomeID     string        `json:"homeId"`
	Resolution string        `json:"resolution"`
	Current    *tibber.Price `json:"current"`
	Today      *daySummary   `json:"today"`
	Tomorrow   *daySummary   `json:"tomorrow"`
	// Items is today's prices and then tomorrow's.
	Items []tibber.Price `json:"items"`
}

// daySummary is the lowest, the highest and the average price of a day.
type daySummary struct {
	Average  float64      `json:"average"`
	Cheapest tibber.Price `json:"cheapest"`
	Dearest  tibber.Price `json:"dearest"`
	Currency string       `json:"currency"`
}

func summarizeDay(prices []tibber.Price) *daySummary {
	var s *daySummary
	sum, n := 0.0, 0
	for _, p := range prices {
		if p.Total == nil {
			continue
		}
		if s == nil {
			s = &daySummary{Cheapest: p, Dearest: p, Currency: p.Currency}
		}
		if *p.Total < *s.Cheapest.Total {
			s.Cheapest = p
		}
		if *p.Total > *s.Dearest.Total {
			s.Dearest = p
		}
		sum += *p.Total
		n++
	}
	if s != nil {
		s.Average = sum / float64(n)
	}
	return s
}

func (a *App) prices(ctx context.Context, c *ctlkit.Call) error {
	if err := a.authorize(ctx); err != nil {
		return err
	}
	home, _, err := a.pickHome(ctx, c)
	if err != nil {
		return err
	}
	resolution := strings.ToUpper(strings.ReplaceAll(c.String("resolution"), "-", "_"))
	info, err := a.Client.Prices(ctx, home.ID, resolution)
	if err != nil {
		return err
	}
	report := priceReport{
		Home: home.Name(), HomeID: home.ID, Resolution: c.String("resolution"), Current: info.Current,
		Today: summarizeDay(info.Today), Tomorrow: summarizeDay(info.Tomorrow),
		Items: append(append([]tibber.Price{}, info.Today...), info.Tomorrow...),
	}
	return c.Print(report, func(w io.Writer) { renderPrices(w, report) })
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
