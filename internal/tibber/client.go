// Package tibber is the client for the Tibber API, which is GraphQL. See
// reference/README.md for what is verified.
package tibber

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/httpx"
)

const (
	service = "Tibber"

	// PageSize is how many nodes one request asks for: a month of hours.
	PageSize = 744
)

// Resolutions of consumption, as the API names them.
const (
	Hourly  = "HOURLY"
	Daily   = "DAILY"
	Weekly  = "WEEKLY"
	Monthly = "MONTHLY"
	Annual  = "ANNUAL"
)

type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
	// Token is the personal access token from developer.tibber.com, sent as
	// a bearer token. It does not run out and cannot be renewed.
	Token string
}

func NewClient() *Client {
	return &Client{HTTP: httpx.NewClient(), BaseURL: "https://api.tibber.com/v1-beta/gql"}
}

// Home is one home on the account.
type Home struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname,omitempty"`
	Address  string `json:"address,omitempty"`
	Postcode string `json:"postcode,omitempty"`
	City     string `json:"city,omitempty"`
	TimeZone string `json:"timeZone"`
	// PriceArea is the bidding zone, such as NO1.
	PriceArea   string `json:"priceArea,omitempty"`
	GridCompany string `json:"gridCompany,omitempty"`
	// RealTime is whether the home has a Pulse or another live meter.
	RealTime bool `json:"realTime"`
	// Subscription is the status of the current agreement, such as "running".
	Subscription string `json:"subscription,omitempty"`
}

// Name is how a home is called in output: its nickname in the app, or else
// its address.
func (h Home) Name() string {
	if h.Nickname != "" {
		return h.Nickname
	}
	if h.Address != "" {
		return h.Address
	}
	return h.ID
}

const homesQuery = `{ viewer { homes {
  id timeZone appNickname
  address { address1 postalCode city }
  meteringPointData { priceAreaCode gridCompany }
  features { realTimeConsumptionEnabled }
  currentSubscription { status }
} } }`

type rawHome struct {
	ID          string `json:"id"`
	TimeZone    string `json:"timeZone"`
	AppNickname string `json:"appNickname"`
	Address     *struct {
		Address1   string `json:"address1"`
		PostalCode string `json:"postalCode"`
		City       string `json:"city"`
	} `json:"address"`
	MeteringPointData *struct {
		PriceAreaCode string `json:"priceAreaCode"`
		GridCompany   string `json:"gridCompany"`
	} `json:"meteringPointData"`
	Features *struct {
		RealTimeConsumptionEnabled bool `json:"realTimeConsumptionEnabled"`
	} `json:"features"`
	CurrentSubscription *struct {
		Status string `json:"status"`
	} `json:"currentSubscription"`
}

func (r rawHome) home() Home {
	h := Home{ID: r.ID, Nickname: strings.TrimSpace(r.AppNickname), TimeZone: r.TimeZone}
	if r.Address != nil {
		h.Address, h.Postcode, h.City = r.Address.Address1, r.Address.PostalCode, r.Address.City
	}
	if r.MeteringPointData != nil {
		h.PriceArea, h.GridCompany = r.MeteringPointData.PriceAreaCode, r.MeteringPointData.GridCompany
	}
	if r.Features != nil {
		h.RealTime = r.Features.RealTimeConsumptionEnabled
	}
	if r.CurrentSubscription != nil {
		h.Subscription = r.CurrentSubscription.Status
	}
	return h
}

// Homes lists the homes on the account.
func (c *Client) Homes(ctx context.Context) ([]Home, error) {
	var answer struct {
		Viewer struct {
			Homes []*rawHome `json:"homes"`
		} `json:"viewer"`
	}
	if err := c.query(ctx, homesQuery, nil, &answer); err != nil {
		return nil, err
	}
	homes := []Home{}
	for _, r := range answer.Viewer.Homes {
		if r != nil {
			homes = append(homes, r.home())
		}
	}
	return homes, nil
}

// Usage is the consumption in one period, as the API returns it. A period
// with no reading yet has nil values.
type Usage struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Consumption is in ConsumptionUnit, which is kWh.
	Consumption     *float64 `json:"consumption"`
	ConsumptionUnit string   `json:"consumptionUnit"`
	// Cost is Consumption times UnitPrice, in Currency.
	Cost *float64 `json:"cost"`
	// UnitPrice is the price per kWh with VAT, and UnitPriceVAT the VAT in it.
	UnitPrice    *float64 `json:"unitPrice"`
	UnitPriceVAT *float64 `json:"unitPriceVAT"`
	Currency     string   `json:"currency"`
}

const usageQuery = `query($home: ID!, $resolution: EnergyResolution!, $first: Int, $after: String) {
  viewer { home(id: $home) {
    consumption(resolution: $resolution, first: $first, after: $after) {
      pageInfo { endCursor hasNextPage }
      nodes { from to consumption consumptionUnit cost unitPrice unitPriceVAT currency }
    }
  } }
}`

// Cursor is the cursor that comes before a time. Tibber's cursors are a
// time in base64.
func Cursor(t time.Time) string {
	return base64.StdEncoding.EncodeToString([]byte(t.Format(time.RFC3339)))
}

// Consumption fetches the periods of a home from the period after the
// cursor on, up to want of them, a page at a time.
func (c *Client) Consumption(ctx context.Context, homeID, resolution, after string, want int) ([]Usage, error) {
	usage := []Usage{}
	for len(usage) < want {
		first := min(want-len(usage), PageSize)
		var answer struct {
			Viewer struct {
				Home struct {
					Consumption *struct {
						PageInfo struct {
							EndCursor   string `json:"endCursor"`
							HasNextPage bool   `json:"hasNextPage"`
						} `json:"pageInfo"`
						Nodes []*Usage `json:"nodes"`
					} `json:"consumption"`
				} `json:"home"`
			} `json:"viewer"`
		}
		vars := map[string]any{"home": homeID, "resolution": resolution, "first": first, "after": after}
		if err := c.query(ctx, usageQuery, vars, &answer); err != nil {
			return nil, err
		}
		page := answer.Viewer.Home.Consumption
		if page == nil {
			return usage, nil
		}
		for _, node := range page.Nodes {
			if node != nil {
				usage = append(usage, *node)
			}
		}
		if !page.PageInfo.HasNextPage || page.PageInfo.EndCursor == "" || page.PageInfo.EndCursor == after || len(page.Nodes) == 0 {
			return usage, nil
		}
		after = page.PageInfo.EndCursor
	}
	return usage, nil
}

// Price is the price of one period.
type Price struct {
	StartsAt string `json:"startsAt"`
	// Total is the price per kWh with VAT, Energy the part that is energy,
	// and Tax the rest.
	Total    *float64 `json:"total"`
	Energy   *float64 `json:"energy"`
	Tax      *float64 `json:"tax"`
	Currency string   `json:"currency"`
	// Level is how the price compares with the last days: VERY_CHEAP,
	// CHEAP, NORMAL, EXPENSIVE or VERY_EXPENSIVE.
	Level string `json:"level"`
}

// PriceInfo is today's and tomorrow's prices. Tomorrow is empty until the
// day-ahead prices are out, in the early afternoon.
type PriceInfo struct {
	Current  *Price  `json:"current"`
	Today    []Price `json:"today"`
	Tomorrow []Price `json:"tomorrow"`
}

const pricesQuery = `query($home: ID!, $resolution: PriceInfoResolution) {
  viewer { home(id: $home) { currentSubscription { priceInfo(resolution: $resolution) {
    current { startsAt total energy tax currency level }
    today { startsAt total energy tax currency level }
    tomorrow { startsAt total energy tax currency level }
  } } } }
}`

// Prices fetches today's and tomorrow's prices of a home. resolution is
// HOURLY or QUARTER_HOURLY.
func (c *Client) Prices(ctx context.Context, homeID, resolution string) (*PriceInfo, error) {
	var answer struct {
		Viewer struct {
			Home struct {
				CurrentSubscription *struct {
					PriceInfo *PriceInfo `json:"priceInfo"`
				} `json:"currentSubscription"`
			} `json:"home"`
		} `json:"viewer"`
	}
	vars := map[string]any{"home": homeID, "resolution": resolution}
	if err := c.query(ctx, pricesQuery, vars, &answer); err != nil {
		return nil, err
	}
	sub := answer.Viewer.Home.CurrentSubscription
	if sub == nil || sub.PriceInfo == nil {
		return nil, ctlkit.NotFoundf("%s has no prices for this home: it has no running agreement", service)
	}
	return sub.PriceInfo, nil
}

// graphQLError is one entry in the errors of an answer.
type graphQLError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

// query sends one GraphQL query and decodes its data into into. GraphQL
// answers 200 also when the query failed, with the reason in errors.
func (c *Client) query(ctx context.Context, query string, vars map[string]any, into any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	body, err := httpx.Do(c.HTTP, req, service, c.UserAgent)
	if err != nil {
		return err
	}

	var answer struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("%s: read the answer: %w", service, err)
	}
	if len(answer.Errors) > 0 {
		return answerError(answer.Errors[0])
	}
	if len(answer.Data) == 0 || string(answer.Data) == "null" {
		return fmt.Errorf("%s: the answer has no data", service)
	}
	if err := json.Unmarshal(answer.Data, into); err != nil {
		return fmt.Errorf("%s: read the answer: %w", service, err)
	}
	return nil
}

// answerError gives a GraphQL error the kind its code means.
func answerError(e graphQLError) error {
	message := strings.TrimSpace(e.Message)
	switch code := e.Extensions.Code; {
	case code == "UNAUTHENTICATED" || code == "UNAUTHORIZED" || code == "FORBIDDEN":
		return ctlkit.Authf("%s: %s", service, message)
	case code == "NOT_FOUND" || strings.Contains(strings.ToLower(message), "not found"):
		return ctlkit.NotFoundf("%s: %s", service, message)
	case code == "TOO_MANY_REQUESTS" || code == "RATE_LIMITED":
		return ctlkit.Errorf(ctlkit.KindRateLimit, "%s: %s", service, message)
	case code == "INTERNAL_SERVER_ERROR" || code == "SERVICE_UNAVAILABLE":
		return ctlkit.Errorf(ctlkit.KindUnavailable, "%s: %s", service, message)
	}
	return fmt.Errorf("%s: %s", service, message)
}
