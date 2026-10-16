# Tibber API, as observed

Endpoint: `POST https://api.tibber.com/v1-beta/gql`, JSON body `{"query", "variables"}`, header `Authorization: Bearer <personal access token>`.

The schema comes from introspection, which answers without a token:

```bash
curl -s https://api.tibber.com/v1-beta/gql -H 'Content-Type: application/json' \
  -d '{"query":"{ __schema { types { name fields { name } } } }"}' | jq .
```

## Verified

- Introspection without a token answers 200 with the schema (2026-10-16).
- `Viewer.homes` and `Viewer.home(id)`. `Home` has `id`, `timeZone`, `appNickname`, `address`, `meteringPointData { priceAreaCode gridCompany }`, `features { realTimeConsumptionEnabled }`, `currentSubscription`.
- `Home.consumption(resolution: EnergyResolution!, first, last, before, after, filterEmptyNodes)`. `EnergyResolution` is `HOURLY DAILY WEEKLY MONTHLY ANNUAL`. There is no quarter-hourly consumption.
- `Consumption` nodes have `from to consumption consumptionUnit cost unitPrice unitPriceVAT currency`. `HomeConsumptionPageInfo` also has `totalCost` and `totalConsumption`.
- `Subscription.priceInfo(resolution: PriceInfoResolution)` with `HOURLY` or `QUARTER_HOURLY`, giving `current`, `today` and `tomorrow`. `priceInfoRange` exists for past prices.
- Mutations: `updateHome`, `sendPushNotification`, and `sendMeterReading`, which is deprecated. The tool uses none.

Checked with a real token on 2026-10-16:

- A bad token answers HTTP 200 with `errors[0].message` "invalid token" and `extensions.code` `UNAUTHENTICATED`, and `data: null`.
- A request without a `User-Agent` is answered too. The tool sends one anyway.
- The `after` cursor is an RFC 3339 time in base64, and it is inclusive: after 00:00 the first hourly node is 00:00 to 01:00, and the first daily node is that day.
- `first` up to 744 works, and paging with `endCursor` works over 92 days of hours (three pages, under a second). An hour still to come has no node: asking past now gives fewer nodes.
- Node times carry the home's offset, such as `2026-09-22T00:00:00.000+02:00`.
- The sum of the hours of a range is the same as the sum of its days, to the øre.
- An hour can be missing in the past: 2207 of 2208 hours from July to September for one home. `usage` counts it as missing.
- Consumption cost is in the currency of the account (NOK for a Norwegian account), also for a home in Sweden. The prices of that home are in SEK.

## Reported by others

- The API wants a `User-Agent` (an open client refuses to run without one). Not so on 2026-10-16.

## Unverified

Check these with the first real token, and move them up:

- The highest `first` above 744.
- Rate limits, and whether a 429 has `Retry-After`.
- Whether `cost` is the energy with VAT and without grid rent, against an invoice.
- How long after an hour its reading comes. At 11:00 the last hour of a home without a live meter was 10:00 to 11:00.
