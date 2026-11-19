package paginate

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib" // Register the database/sql driver used by pgtestdb.
)

// pgtestdb gives each test an isolated database and handles its cleanup.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PGPROTOPAGINATE_TEST_DSN")
	if testing.Short() && dsn == "" {
		t.Skip("PostgreSQL tests require PGPROTOPAGINATE_TEST_DSN in short mode")
	}
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:15433/pgprotopaginate?sslmode=disable"
	}
	connection, err := url.Parse(dsn)
	require.NoError(t, err)
	require.NotNil(t, connection.User, "test DSN must include a database user")
	port := connection.Port()
	if port == "" {
		port = "5432"
	}
	password, _ := connection.User.Password()
	config := pgtestdb.Custom(t, pgtestdb.Config{
		DriverName: "pgx",
		Host:       connection.Hostname(),
		Port:       port,
		User:       connection.User.Username(),
		Password:   password,
		Database:   strings.TrimPrefix(connection.Path, "/"),
		Options:    connection.RawQuery,
	}, pgtestdb.NoopMigrator{})
	pool, err := pgxpool.New(t.Context(), config.URL())
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
