package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/ctltest"
	"github.com/langtind/ctlkit/keystore"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/tibberctl/internal/tibber"
)

// harness runs tibberctl against a fake Tibber, with the token in memory.
type harness struct {
	t      *testing.T
	fake   *ctltest.Fake
	tokens *keystore.Memory
	env    map[string]string
	// refuse makes the fake answer every query as Tibber answers a bad token.
	refuse bool
}

var (
	oslo, _ = time.LoadLocation("Europe/Oslo")
	// testTime is 15:00 on 12 October 2026, so yesterday is the 11th.
	testTime = time.Date(2026, 10, 12, 15, 0, 0, 0, oslo)
)

// The GraphQL endpoint is one path, and every query is a POST to it.
const endpoint = "POST /"

const homesAnswer = `{"data":{"viewer":{"homes":[
  {"id":"h-cabin","timeZone":"Europe/Oslo","appNickname":"Fjellstua","address":{"address1":"Hytteveien 1","postalCode":"9999","city":"Lillevik"},"meteringPointData":{"priceAreaCode":"NO1","gridCompany":"Nett AS"},"features":{"realTimeConsumptionEnabled":false},"currentSubscription":{"status":"running"}},
  {"id":"h-flat","timeZone":"Europe/Oslo","appNickname":"","address":{"address1":"Strandgata 2","postalCode":"0001","city":"Oslo"},"meteringPointData":{"priceAreaCode":"NO1","gridCompany":"Nett AS"},"features":{"realTimeConsumptionEnabled":true},"currentSubscription":{"status":"running"}}
]}}}`

type request struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, fake: ctltest.NewFake(t), tokens: &keystore.Memory{Secret: "stored-token"}, env: map[string]string{}}
	h.fake.Check = func(t *testing.T, c ctltest.Call) {
		if got := c.Header.Get("User-Agent"); got != Name+"/1.2.3 (+https://github.com/langtind/"+Name+")" {
			t.Errorf("User-Agent = %q", got)
		}
	}
	h.fake.Handle(endpoint, func(c ctltest.Call) ctltest.Answer {
		var r request
		if err := json.Unmarshal([]byte(c.Body), &r); err != nil {
			return ctltest.Answer{Status: http.StatusBadRequest, Body: `{"errors":[{"message":"bad request"}]}`}
		}
		if h.refuse {
			return ctltest.Answer{Body: `{"errors":[{"message":"Context creation failed: invalid token","extensions":{"code":"UNAUTHENTICATED"}}]}`}
		}
		switch {
		case strings.Contains(r.Query, "homes"):
			return ctltest.Answer{Body: homesAnswer}
		case strings.Contains(r.Query, "consumption"):
			return ctltest.Answer{Body: consumption(t, r.Variables)}
		case strings.Contains(r.Query, "priceInfo"):
			return ctltest.Answer{Body: pricesAnswer}
		}
		return ctltest.Answer{Body: `{"errors":[{"message":"unknown query"}]}`}
	})
	return h
}

// consumption makes up the periods from the cursor on, as Tibber pages
// them: the first is the one that starts at the cursor.
// An hour uses 1 kWh and a tenth for each hour of the day, except 18:00,
// which uses 4.2. A kWh costs 2. Periods after testTime have no reading.
func consumption(t *testing.T, vars map[string]any) string {
	cursor, err := base64.StdEncoding.DecodeString(vars["after"].(string))
	if err != nil {
		t.Fatalf("cursor %v: %v", vars["after"], err)
	}
	after, err := time.Parse(time.RFC3339, string(cursor))
	if err != nil {
		t.Fatalf("cursor %q: %v", cursor, err)
	}
	resolution, first := vars["resolution"].(string), int(vars["first"].(float64))
	if first > tibber.PageSize {
		t.Errorf("first = %d, more than a page", first)
	}

	nodes := []string{}
	from := after.In(oslo)
	for range first {
		var to time.Time
		var kwh float64
		switch resolution {
		case tibber.Hourly:
			from = from.Truncate(time.Hour)
			to = from.Add(time.Hour)
			kwh = 1 + float64(from.Hour())/10
			if from.Hour() == 18 {
				kwh = 4.2
			}
		case tibber.Daily:
			from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, oslo)
			to = from.AddDate(0, 0, 1)
			kwh = 10
		default:
			t.Fatalf("resolution %s", resolution)
		}
		if from.Before(after) {
			from = to
			continue
		}
		if to.After(testTime) {
			nodes = append(nodes, fmt.Sprintf(`{"from":%q,"to":%q,"consumption":null,"consumptionUnit":"kWh","cost":null,"unitPrice":2,"unitPriceVAT":0.4,"currency":"NOK"}`, from.Format(time.RFC3339), to.Format(time.RFC3339)))
		} else {
			nodes = append(nodes, fmt.Sprintf(`{"from":%q,"to":%q,"consumption":%g,"consumptionUnit":"kWh","cost":%g,"unitPrice":2,"unitPriceVAT":0.4,"currency":"NOK"}`, from.Format(time.RFC3339), to.Format(time.RFC3339), kwh, kwh*2))
		}
		from = to
	}
	return fmt.Sprintf(`{"data":{"viewer":{"home":{"consumption":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[%s]}}}}}`, strings.Join(nodes, ","))
}

const pricesAnswer = `{"data":{"viewer":{"home":{"currentSubscription":{"priceInfo":{
  "current":{"startsAt":"2026-10-12T15:00:00.000+02:00","total":1.5,"energy":1.1,"tax":0.4,"currency":"NOK","level":"NORMAL"},
  "today":[
    {"startsAt":"2026-10-12T00:00:00.000+02:00","total":0.8,"energy":0.5,"tax":0.3,"currency":"NOK","level":"CHEAP"},
    {"startsAt":"2026-10-12T15:00:00.000+02:00","total":1.5,"energy":1.1,"tax":0.4,"currency":"NOK","level":"NORMAL"},
    {"startsAt":"2026-10-12T18:00:00.000+02:00","total":2.5,"energy":1.9,"tax":0.6,"currency":"NOK","level":"VERY_EXPENSIVE"}],
  "tomorrow":[]}}}}}}`

// program returns tibberctl wired to the fake service.
func (h *harness) program() *ctlkit.Program {
	app := &App{
		Client:  &tibber.Client{HTTP: h.fake.Client(), BaseURL: h.fake.URL() + "/"},
		Session: &session.Manager{Store: h.tokens},
		Getenv:  func(name string) string { return h.env[name] },
		Now:     func() time.Time { return testTime },
		Zone:    oslo,
	}
	return app.program(ctlkit.Build{Version: "1.2.3"}, os.DirFS("../../skills/"+Name))
}

// run runs one command. h.fake.Calls holds what it sent.
func (h *harness) run(stdin string, args ...string) (code int, stdout, stderr string) {
	h.t.Helper()
	h.fake.Reset()
	return ctltest.Run(h.program(), stdin, args...)
}

// sentVariables returns the variables of the query sent last.
func (h *harness) sentVariables() map[string]any {
	h.t.Helper()
	var r request
	if err := json.Unmarshal([]byte(h.fake.Calls[len(h.fake.Calls)-1].Body), &r); err != nil {
		h.t.Fatal(err)
	}
	return r.Variables
}

func TestContract(t *testing.T) {
	ctltest.Contract(t, newHarness(t).program, "../../skills/"+Name+"/SKILL.md", "../../README.md")
}

func TestHomes(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("", "homes")
	want := "# Homes\n\n" +
		"- Fjellstua\n  Address: Hytteveien 1, 9999 Lillevik\n  Price area: NO1\n  Grid company: Nett AS\n  Agreement: running\n  id: h-cabin\n" +
		"- Strandgata 2\n  Price area: NO1\n  Grid company: Nett AS\n  Agreement: running\n  Live meter: yes\n  id: h-flat\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if len(h.fake.Calls) != 1 || h.fake.Calls[0].Token != "stored-token" {
		t.Errorf("calls = %+v", h.fake.Calls)
	}

	_, stdout, _ = h.run("", "homes", "--json", "--fields", "id,nickname")
	if got := strings.Join(strings.Fields(stdout), ""); got != `{"items":[{"id":"h-cabin","nickname":"Fjellstua"},{"id":"h-flat"}]}` {
		t.Errorf("json = %s", got)
	}
}

func TestUsageOfYesterdayHasTheTotalsAndThePeak(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("", "usage", "--home", "fjell", "--json")
	var r usageReport
	ctltest.OneJSON(t, stdout, &r)
	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	// 24 hours of 1 + h/10 kWh, with 18:00 at 4.2 instead of 2.8.
	if r.Home != "Fjellstua" || r.From != "2026-10-11" || r.To != "2026-10-11" || r.Periods != 24 || r.Missing != 0 ||
		fmt.Sprintf("%.2f %.2f %.4f", r.Consumption, r.Cost, *r.AveragePrice) != "53.00 106.00 2.0000" || r.Currency != "NOK" {
		t.Errorf("report = %+v", r)
	}
	if r.Peak == nil || r.Peak.From != "2026-10-11T18:00:00+02:00" || *r.Peak.Consumption != 4.2 || len(r.Items) != 24 || r.Items[0].From != "2026-10-11T00:00:00+02:00" {
		t.Errorf("peak = %+v, items = %d", r.Peak, len(r.Items))
	}
	if v := h.sentVariables(); v["home"] != "h-cabin" || v["resolution"] != "HOURLY" {
		t.Errorf("variables = %v", v)
	}

	_, stdout, _ = h.run("", "usage", "--home", "Fjellstua", "--limit", "2")
	for _, line := range []string{"# Fjellstua, 2026-10-11\n", "Consumption: 53.00 kWh\n", "Cost: 106.00 NOK\n", "Peak: 4.20 kWh, 2026-10-11 18:00-19:00\n", "| 2026-10-11 00:00-01:00 | 1.00 | 2.00 | 2.0000 |\n", "Raise --limit"} {
		if !strings.Contains(stdout, line) {
			t.Errorf("markdown lacks %q:\n%s", line, stdout)
		}
	}
}

func TestUsageSortedByConsumptionGivesTheHighestHours(t *testing.T) {
	h := newHarness(t)
	_, stdout, _ := h.run("", "usage", "--home", "fjellstua", "--sort", "consumption", "--limit", "2", "--json")
	var r usageReport
	ctltest.OneJSON(t, stdout, &r)
	if !r.Truncated || len(r.Items) != 2 || r.Items[0].From != "2026-10-11T18:00:00+02:00" || r.Items[1].From != "2026-10-11T23:00:00+02:00" || r.Periods != 24 {
		t.Errorf("report = %+v", r)
	}
}

func TestUsageOfTodayCountsTheHoursToComeAsMissing(t *testing.T) {
	h := newHarness(t)
	_, stdout, _ := h.run("", "usage", "--home", "h-cabin", "--from", "today", "--json")
	var r usageReport
	ctltest.OneJSON(t, stdout, &r)
	if r.Periods != 15 || r.Missing != 9 || len(r.Items) != 24 {
		t.Errorf("periods = %d, missing = %d, items = %d", r.Periods, r.Missing, len(r.Items))
	}
}

func TestUsageOfARangeOfDays(t *testing.T) {
	h := newHarness(t)
	_, stdout, _ := h.run("", "usage", "--home", "strandgata", "--from", "2026-09-22", "--to", "2026-09-25", "--resolution", "daily", "--json")
	var r usageReport
	ctltest.OneJSON(t, stdout, &r)
	if r.HomeID != "h-flat" || r.Periods != 4 || r.Consumption != 40 || r.Cost != 80 || r.Items[0].From != "2026-09-22T00:00:00+02:00" || r.Items[3].From != "2026-09-25T00:00:00+02:00" {
		t.Errorf("report = %+v", r)
	}
	if v := h.sentVariables(); v["resolution"] != "DAILY" {
		t.Errorf("variables = %v", v)
	}
}

func TestUsageNeedsAHomeWhenThereAreTwo(t *testing.T) {
	h := newHarness(t)
	code, _, stderr := h.run("", "usage")
	if code != 2 || !strings.Contains(stderr, `the account has 2 homes, so --home is needed: "Fjellstua", "Strandgata 2"`) {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}

	code, stdout, _ := h.run("", "usage", "--home", "nowhere", "--json")
	var doc ctltest.ErrorDocument
	ctltest.OneJSON(t, stdout, &doc)
	if code != 3 || doc.Error.Kind != "not_found" || doc.Error.Hint != `the homes are "Fjellstua", "Strandgata 2".` {
		t.Errorf("exit = %d, error = %+v", code, doc.Error)
	}
}

func TestUsageRefusesABadRange(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"--from", "2026-09-25", "--to", "2026-09-22"},
		{"--from", "22.09.2026"},
		{"--from", "2026-01-01", "--to", "2026-12-31"},
	} {
		code, _, stderr := h.run("", append([]string{"usage", "--home", "fjellstua"}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit = %d, stderr = %q", args, code, stderr)
		}
	}
}

func TestAnHourIsMissedOrAddedWhenTheClockChanges(t *testing.T) {
	autumn := time.Date(2026, 10, 25, 0, 0, 0, 0, oslo)
	spring := time.Date(2026, 3, 29, 0, 0, 0, 0, oslo)
	if got := count("hourly", autumn, autumn.AddDate(0, 0, 1)); got != 25 {
		t.Errorf("autumn = %d", got)
	}
	if got := count("hourly", spring, spring.AddDate(0, 0, 1)); got != 23 {
		t.Errorf("spring = %d", got)
	}
}

func TestPrices(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("", "prices", "--home", "fjellstua")
	for _, line := range []string{"Now: 1.5000 NOK/kWh (normal)\n", "Today: average 1.6000, cheapest 0.8000 at 00:00, dearest 2.5000 at 18:00 NOK/kWh\n", "Tomorrow: not out yet\n", "| 2026-10-12 18:00 | 2.5000 | very expensive |\n"} {
		if !strings.Contains(stdout, line) {
			t.Errorf("exit = %d, stderr = %q, markdown lacks %q:\n%s", code, stderr, line, stdout)
		}
	}
	if v := h.sentVariables(); v["resolution"] != "HOURLY" {
		t.Errorf("variables = %v", v)
	}
	h.run("", "prices", "--home", "fjellstua", "--resolution", "quarter-hourly")
	if v := h.sentVariables(); v["resolution"] != "QUARTER_HOURLY" {
		t.Errorf("variables = %v", v)
	}
}

func TestWithoutATokenNothingIsSent(t *testing.T) {
	h := newHarness(t)
	h.tokens.Secret = ""
	code, _, stderr := h.run("", "homes")
	if code != 4 || stderr != "Error: not logged in\nHint: run \"tibberctl login\", or set TIBBERCTL_TOKEN.\n" || len(h.fake.Calls) != 0 {
		t.Errorf("exit = %d, stderr = %q, calls = %+v", code, stderr, h.fake.Calls)
	}

	h.env[envToken] = "env-token"
	if code, _, _ := h.run("", "homes"); code != 0 || h.fake.Calls[0].Token != "env-token" {
		t.Errorf("with %s: exit = %d, calls = %+v", envToken, code, h.fake.Calls)
	}
}

func TestARefusedTokenSaysWhatToDo(t *testing.T) {
	h := newHarness(t)
	h.refuse = true
	code, _, stderr := h.run("", "homes")
	if code != 4 || !strings.Contains(stderr, "Error: Tibber: Context creation failed: invalid token\nHint: the token may have been revoked.") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestLoginChecksTheTokenBeforeItStoresIt(t *testing.T) {
	h := newHarness(t)
	h.tokens.Secret = ""
	code, stdout, stderr := h.run("new-token\n", "login", "--token-stdin")
	if code != 0 || stdout != "Logged in. The token is in the system keyring. The account has 2 homes.\n" || h.fake.Calls[0].Token != "new-token" {
		t.Errorf("exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	m := session.Manager{Store: h.tokens}
	if s, _ := m.Load(); s.AccessToken != "new-token" || !s.AccessExpires.IsZero() {
		t.Errorf("stored = %+v", s)
	}

	h.tokens.Secret = ""
	h.refuse = true
	code, _, stderr = h.run("bad-token\n", "login", "--token-stdin")
	if code != 4 || !strings.Contains(stderr, "Tibber refused the token") || h.tokens.Secret != "" {
		t.Errorf("refused: exit = %d, stderr = %q, stored = %q", code, stderr, h.tokens.Secret)
	}
}

func TestStatusAndLogout(t *testing.T) {
	h := newHarness(t)
	if _, stdout, _ := h.run("", "status"); stdout != "Logged in. The token is in the system keyring and does not run out.\n" || len(h.fake.Calls) != 0 {
		t.Errorf("status = %q", stdout)
	}
	if code, _, _ := h.run("", "logout"); code != 0 || h.tokens.Secret != "" {
		t.Errorf("logout: exit = %d", code)
	}
	if _, stdout, _ := h.run("", "status"); stdout != "Not logged in. Run \"tibberctl login\".\n" {
		t.Errorf("status = %q", stdout)
	}
}

func TestAHomeIsFoundWithoutTheMarksOnItsLetters(t *testing.T) {
	homes := []tibber.Home{{ID: "a", Nickname: "Sjöbäcken"}, {ID: "b", Nickname: "Mølleåsen"}, {ID: "c", Nickname: "Hytta"}, {ID: "d", Nickname: "Hytta 2"}}
	for want, id := range map[string]string{"sjobacken": "a", "SJÖBÄCKEN": "a", "molleasen": "b", "moelle": "", "hytta": "c", "Hytta 2": "d"} {
		home, err := matchHome(homes, want)
		if id == "" {
			if err == nil {
				t.Errorf("%q found %q", want, home.ID)
			}
			continue
		}
		if err != nil || home.ID != id {
			t.Errorf("%q: home = %q, err = %v", want, home.ID, err)
		}
	}
}
