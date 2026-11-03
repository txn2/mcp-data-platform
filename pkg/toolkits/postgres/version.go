package postgres

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// MinServerMajor is the oldest PostgreSQL major an application database may
// be on (#2447). A connection to an older server is refused when it is saved,
// when it is tested and on every physical connection its pool opens, so a
// connection configured in a file is held to it as one saved through the
// admin API is.
const MinServerMajor = 17

// serverTooOldError refuses a server older than MinServerMajor, naming the
// version it reported.
type serverTooOldError struct {
	Version string
	Major   int
}

func (e *serverTooOldError) Error() string {
	return fmt.Sprintf("postgres: the server is PostgreSQL %s; an application database needs PostgreSQL %d or later",
		e.Version, MinServerMajor)
}

// majorOf reads the major from a server_version ("17.6", "18beta1",
// "16.4 (Debian 16.4-1)").
func majorOf(version string) (int, error) {
	digits := version
	if i := strings.IndexFunc(version, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		digits = version[:i]
	}
	major, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("postgres: reading the server's version from %q: %w", version, err)
	}
	return major, nil
}

// floor refuses a server_version older than MinServerMajor.
func floor(version string) error {
	major, err := majorOf(version)
	if err != nil {
		return err
	}
	if major < MinServerMajor {
		return &serverTooOldError{Version: version, Major: major}
	}
	return nil
}

// checkFloor refuses a connection whose server, by the server_version it
// reported when the connection opened, is older than MinServerMajor.
func checkFloor(pc *pgconn.PgConn) error {
	return floor(strings.TrimSpace(pc.ParameterStatus("server_version")))
}

// tooOld reports the floor refusal err carries, if it carries one.
func tooOld(err error) (*serverTooOldError, bool) {
	var e *serverTooOldError
	ok := errors.As(err, &e)
	return e, ok
}
