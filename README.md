# tibberctl

A command line tool for electricity consumption, cost and prices at [Tibber](https://tibber.com), per home.

It answers questions like "what did power cost at the cabin from 22 to 25 September" and "what was the peak hour yesterday". It reads only, through Tibber's official API, and changes nothing.

The output is Markdown, so it reads well in a terminal and when an AI agent relays it into a chat. Every command also prints JSON.

> tibberctl is unofficial and not affiliated with Tibber.

## Install

```bash
brew install langtind/tap/tibberctl
```

Or with Go:

```bash
go install github.com/langtind/tibberctl@latest
```

## Log in

Make a personal access token at [developer.tibber.com](https://developer.tibber.com/settings/access-token), logged in with your Tibber account. Then:

```bash
tibberctl login
```

It asks for the token, checks it against Tibber and stores it in the system keyring. The token does not run out. In a script, pipe it in:

```bash
op read "op://vault/Tibber/token" | tibberctl login --token-stdin
```

Set `TIBBERCTL_TOKEN` to use a token without the keyring.

## Use

```bash
tibberctl homes
tibberctl usage --home cabin                                     # yesterday, hour by hour, with totals and the peak
tibberctl usage --home cabin --from 2026-09-22 --to 2026-09-25   # both days included
tibberctl usage --home cabin --sort consumption --limit 3        # yesterday's three highest hours
tibberctl usage --home cabin --from 2026-01-01 --to 2026-09-30 --resolution monthly
tibberctl prices --home cabin                                    # today and tomorrow
```

`--home` takes the id, or a part of the nickname, the address or the city. With one home on the account it can be left out.

Cost is what Tibber charges for the energy, with VAT. Grid rent from the grid company, Tibber's monthly fee and state support are not in it.

## For agents

```bash
tibberctl install-skill   # copies the skill to ~/.claude/skills
tibberctl schema          # every command, flag and error kind as JSON
```

Add `--json` to a command for one JSON document on stdout, and `--fields from,consumption` to keep only some fields of the periods. A failed command exits with a code that says why: 2 wrong call, 3 not found, 4 not logged in, 6 rate limit, 7 service unavailable. With `--json` the error is a JSON document too.

## Develop

```bash
go build -o tibberctl .
make check
```

## License

MIT
