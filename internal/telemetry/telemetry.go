// Package telemetry reports crashes and server errors to Sentry, from official builds only.
//
// Release builds set the DSN with -ldflags "-X …/internal/telemetry.dsn=…"; a build from
// source has none and sends nothing. HEALTHSYNC_SENTRY_DSN sets another DSN, and
// HEALTHSYNC_NO_TELEMETRY=1 (or the desktop app's setting) turns reporting off.
//
// Reports carry the error, its stack trace, the version and the OS, never health data:
// requests, user, host name, breadcrumbs and extra data are removed before sending.
package telemetry

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

var dsn string // set by release builds

var enabled bool

// Init starts reporting for component ("server", "desktop", "cli") at version. It does
// nothing without a DSN or when off is true. Call the returned function before exiting,
// to send what is queued.
func Init(component, version string, off bool) (flush func()) {
	flush = func() {}
	d := dsn
	if v := os.Getenv("HEALTHSYNC_SENTRY_DSN"); v != "" {
		d = v
	}
	if d == "" || off || truthy(os.Getenv("HEALTHSYNC_NO_TELEMETRY")) {
		return
	}
	env := "production"
	if version == "" || version == "dev" || strings.Contains(version, "-dev") {
		env = "development"
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              d,
		Release:          "healthsync@" + strings.TrimPrefix(version, "v"),
		Environment:      env,
		ServerName:       component, // not the host name, which is often a person's name
		AttachStacktrace: true,
		SendDefaultPII:   false,
		BeforeSend:       scrub,
		BeforeBreadcrumb: func(*sentry.Breadcrumb, *sentry.BreadcrumbHint) *sentry.Breadcrumb { return nil },
	})
	if err != nil {
		return
	}
	enabled = true
	sentry.ConfigureScope(func(s *sentry.Scope) {
		s.SetTag("component", component)
		s.SetTag("os", runtime.GOOS+"/"+runtime.GOARCH)
	})
	return func() { sentry.Flush(3 * time.Second) }
}

// scrub keeps only what a crash report needs.
func scrub(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	e.Request = nil
	e.User = sentry.User{}
	e.Breadcrumbs = nil
	e.ServerName = ""
	return e
}

// Error reports err, with where it happened.
func Error(where string, err error) {
	if !enabled || err == nil {
		return
	}
	sentry.WithScope(func(s *sentry.Scope) {
		s.SetTag("where", where)
		sentry.CaptureException(err)
	})
}

// Errorf reports a message as an error.
func Errorf(where, msg string) { Error(where, errors.New(msg)) }

// Recover reports a panic and panics again; use it as `defer telemetry.Recover()` at the
// top of a goroutine or of main.
func Recover() {
	if !enabled {
		return
	}
	if r := recover(); r != nil {
		sentry.CurrentHub().Recover(r)
		sentry.Flush(3 * time.Second)
		panic(r)
	}
}

// Enabled reports whether reports are sent.
func Enabled() bool { return enabled }

func truthy(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "yes"
}
