// Package cli implements the tibberctl command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/keystore"
	"github.com/langtind/ctlkit/session"

	"github.com/langtind/tibberctl/internal/tibber"
)

const (
	// Name is the program name. A rename also touches the module path, the
	// skill directory with its embed line in skill_embed.go, and the docs.
	Name = "tibberctl"

	summary = "read electricity consumption, cost and prices at Tibber"

	// envToken names the variable that overrides the keyring. Cron jobs and
	// containers have no keyring to unlock.
	envToken = "TIBBERCTL_TOKEN"
)

// App holds what the commands work with. Tests replace the fields to point
// tibberctl at a fake service and at a keyring in memory.
type App struct {
	Client *tibber.Client
	// Session holds the token. A personal access token does not run out, so
	// it is never renewed.
	Session *session.Manager
	Getenv  func(string) string
	// Now is the clock, and Zone the time zone that times are shown in.
	Now  func() time.Time
	Zone *time.Location
}

// New returns tibberctl as it runs for real.
func New(build ctlkit.Build, skill fs.FS) *ctlkit.Program {
	app := &App{
		Client:  tibber.NewClient(),
		Session: &session.Manager{Store: keystore.Keyring(Name, "token")},
		Getenv:  os.Getenv,
		Now:     time.Now,
		Zone:    time.Local,
	}
	return app.program(build, skill)
}

func (a *App) program(build ctlkit.Build, skill fs.FS) *ctlkit.Program {
	p := ctlkit.New(Name, summary, build)
	p.URL = "https://github.com/langtind/" + Name
	p.Skill = skill
	p.Commands = a.commands()
	p.Hint = hint
	a.Client.UserAgent = p.UserAgent()
	a.Session.Now = a.Now
	return p
}

// hint says what to do about an error that cannot say it itself.
func hint(err error) string {
	if ctlkit.KindOf(err) == ctlkit.KindAuth {
		return fmt.Sprintf(`the token may have been revoked. Make a new one at https://developer.tibber.com/settings/access-token and run "%s login".`, Name)
	}
	return ""
}

// authorize gives the client a token: the one from the environment, or else
// the stored one.
func (a *App) authorize(ctx context.Context) error {
	if token := strings.TrimSpace(a.Getenv(envToken)); token != "" {
		a.Client.Token = token
		return nil
	}
	token, err := a.Session.Token(ctx)
	if errors.Is(err, session.ErrNotLoggedIn) {
		return ctlkit.Authf("%w", err).WithHint(`run "%s login", or set %s.`, Name, envToken)
	}
	if err != nil {
		return err
	}
	a.Client.Token = token
	return nil
}
