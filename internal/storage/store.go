package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/yernsun/network-service-composer/internal/model"
)

var (
	ErrNotFound     = errors.New("resource not found")
	ErrConflict     = errors.New("resource conflict")
	ErrPrecondition = errors.New("resource revision precondition failed")
)

type Dialect string

const (
	Postgres Dialect = "postgres"
	MySQL    Dialect = "mysql"
	SQLite   Dialect = "sqlite"
)

type Page struct {
	Limit int
	After string
}

func (p Page) normalized() Page {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	return p
}

type Store interface {
	ResourceCounts(context.Context) (map[string]int64, error)
	ReencryptControlDocument(context.Context, ControlDocument, json.RawMessage) error
	GetControlDocument(context.Context, string) (ControlDocument, error)
	ListControlDocuments(context.Context, string, string, Page) ([]ControlDocument, error)
	GetNotificationWork(context.Context, string, string, string) (ControlDocument, error)
	ListNotificationWork(context.Context, string, time.Time, []string, int) ([]ControlDocument, error)
	NotificationWorkCounts(context.Context) ([]NotificationWorkCount, error)
	WriteControlDocuments(context.Context, []ControlWrite) error
	PublishNginxChanges(context.Context, string, int64, []ControlWrite, []RouteWrite) error
	WriteApplicationDocuments(context.Context, string, int64, []ControlWrite) error
	DeleteEmptyEnvironment(context.Context, string, string, int64) error
	MutateProjectLifecycle(context.Context, ProjectLifecycleMutation) error
	CheckProjectResource(context.Context, string) error
	PublishNginxDocuments(context.Context, string, int64, []ControlWrite) error
	PruneControlHistory(context.Context, string, string, int, []string) error
	ServiceEnvironments(context.Context, []string) (map[string]model.EnvironmentReference, error)
	Ping(context.Context) error
	Migrate(context.Context) error
	Close() error
	Dialect() Dialect

	CreatePool(context.Context, *model.GatewayPool) error
	ListPools(context.Context, Page) ([]model.GatewayPool, error)
	GetPool(context.Context, string) (model.GatewayPool, error)
	UpdatePool(context.Context, *model.GatewayPool, int64) error
	DeletePool(context.Context, string, int64) error
	BumpPoolRevision(context.Context, string) (int64, error)

	CreateRuntime(context.Context, *model.Runtime) error
	ListRuntimes(context.Context, Page) ([]model.Runtime, error)
	GetRuntime(context.Context, string) (model.Runtime, error)
	GetDockerResource(context.Context, string, string, string) (DockerResource, error)
	GetDockerResourceByOperation(context.Context, string) (DockerResource, error)
	ListDockerResources(context.Context, string, string, Page) ([]DockerResource, error)
	RecordDockerResource(context.Context, DockerResource) error
	DeleteDockerResource(context.Context, string, string, string) error
	HasPendingDockerResourceConsumer(context.Context, string, string) (bool, error)
	UpdateRuntime(context.Context, *model.Runtime, int64) error
	DeleteRuntime(context.Context, string, int64) error
	NextObservation(context.Context, string) (int64, error)
	ApplyRuntimeSnapshot(context.Context, model.RuntimeSnapshot) (bool, int64, error)
	MarkRuntimeOffline(context.Context, string, int64, string, time.Time) (bool, error)
	SweepEndpointLifecycle(context.Context, string, int64, string, time.Time, time.Duration, time.Duration, time.Duration) (bool, int64, error)

	UpsertService(context.Context, *model.Service) error
	ListServices(context.Context, Page) ([]model.Service, error)
	GetService(context.Context, string) (model.Service, error)
	MarkServiceRuntimeRemoved(context.Context, string, time.Time) error
	UpdateService(context.Context, *model.Service, int64) error
	ListEndpoints(context.Context, string) ([]model.Endpoint, error)
	UpsertEndpoint(context.Context, *model.Endpoint) error
	UpdateServiceProbe(context.Context, model.ProbeResult) (bool, error)
	ListServiceGroups(context.Context, string, ConsoleFilter, Page) ([]ServiceGroup, error)
	ListRoutesFiltered(context.Context, ConsoleFilter, Page) ([]model.Route, error)
	ListEndpointsPage(context.Context, string, Page) ([]model.Endpoint, error)
	ConsoleReferences(context.Context, string, []string) (map[string]ConsoleReference, error)
	ListServicesFiltered(context.Context, ServiceFilter, Page) ([]model.Service, error)
	ListAllServicesFiltered(context.Context, ServiceFilter) ([]model.Service, error)
	ActivateApplicationRevision(context.Context, string, string, string, string, []string, map[string]int, string, time.Time) error
	ActivateServiceRevision(context.Context, string, string, string, string, []string, map[string]int, string, time.Time) error
	DeferServiceRevision(context.Context, string, string, string, model.ApplicationStatus, time.Time) error

	CreateDomain(context.Context, *model.Domain) error
	CreateManagedDomain(context.Context, *model.Domain, *model.Certificate) error
	ListDomains(context.Context, Page) ([]model.Domain, error)
	GetDomain(context.Context, string) (model.Domain, error)
	UpdateDomain(context.Context, *model.Domain, int64) error
	DeleteDomain(context.Context, string, int64) error

	CreateRoute(context.Context, *model.Route) error
	ListRoutes(context.Context, Page) ([]model.Route, error)
	GetRoute(context.Context, string) (model.Route, error)
	UpdateRoute(context.Context, *model.Route, int64) error
	DeleteRoute(context.Context, string, int64) error
	ListServiceDomainBindings(context.Context, string) ([]model.DomainBinding, error)
	CreateServiceDomainBinding(context.Context, *model.Domain, *model.Certificate, *model.Route, *model.DeploymentOperation, *model.Service, int64) error
	CreateServiceDomainBindingWithDraft(context.Context, *model.Domain, *model.Certificate, *model.Route, *model.DeploymentOperation, *model.Service, int64, ControlWrite) error
	CreateServiceDomainBindingWithIntent(context.Context, *model.Domain, *model.Certificate, *model.Route, *model.DeploymentOperation, ControlWrite, int64) error
	CreateDeploymentOperationWithReceipt(context.Context, *model.DeploymentOperation, ControlWrite, int64) error
	UpdateServiceDomainBinding(context.Context, string, *model.Domain, *model.Certificate, *model.Route, int64, int64) error
	DisableServiceDomainBinding(context.Context, string, string, int64, time.Time, *model.DeploymentOperation) error
	SetApplicationRouteStatus(context.Context, string, model.RouteStatus, string, time.Time) error
	SetApplicationRouteStatusByID(context.Context, string, string, model.RouteStatus, string, time.Time) error
	CompleteApplicationDomainDetach(context.Context, string, string, time.Time) error

	CreateCatalogSource(context.Context, *model.CatalogSource) error
	ListCatalogSources(context.Context, Page) ([]model.CatalogSource, error)
	GetCatalogSource(context.Context, string) (model.CatalogSource, error)
	UpdateCatalogSource(context.Context, *model.CatalogSource, int64) error
	UpsertCatalogAppVersion(context.Context, *model.CatalogAppVersion) error
	SyncCatalogAppVersions(context.Context, *model.CatalogSource, []model.CatalogAppVersion, int64) error
	ListCatalogAppVersions(context.Context, string, string, Page) ([]model.CatalogAppVersion, error)
	GetCatalogAppVersion(context.Context, string) (model.CatalogAppVersion, error)

	ReconcileSystemApplications(context.Context, string, []SystemApplicationRegistration, []ControlWrite) error
	CreateApplication(context.Context, *model.Application, *model.ApplicationRevision, *model.DeploymentOperation) error
	CreateApplicationInstall(context.Context, *model.Application, *model.ApplicationRevision, []model.Service, *model.Domain, *model.Certificate, *model.Route, *model.DeploymentOperation) error
	ListApplications(context.Context, string, Page) ([]model.Application, error)
	GetApplication(context.Context, string) (model.Application, error)
	UpdateApplication(context.Context, *model.Application, int64) error
	SetApplicationStatus(context.Context, string, model.ApplicationStatus, string, time.Time) error
	FailServiceRevision(context.Context, string, string, model.ApplicationStatus, string, time.Time) error
	SetApplicationReplicaOverride(context.Context, string, string, int, time.Time) error
	CreateApplicationRevision(context.Context, *model.ApplicationRevision, *model.DeploymentOperation, int64, time.Time) error
	ListApplicationRevisions(context.Context, string, Page) ([]model.ApplicationRevision, error)
	GetApplicationRevision(context.Context, string) (model.ApplicationRevision, error)
	CreateDeploymentOperation(context.Context, *model.DeploymentOperation) (model.DeploymentOperation, error)
	ListDeploymentOperations(context.Context, string, Page) ([]model.DeploymentOperation, error)
	GetDeploymentOperation(context.Context, string) (model.DeploymentOperation, error)
	GetDeploymentOperationScope(context.Context, string) (string, string, bool, error)
	GetDeploymentOperationByIdempotency(context.Context, string, string) (model.DeploymentOperation, error)
	ClaimDeploymentOperation(context.Context, string, time.Time, time.Time) (model.DeploymentOperation, error)
	HeartbeatDeploymentOperation(context.Context, string, string, time.Time) error
	FinishDeploymentOperation(context.Context, *model.DeploymentOperation, time.Time) error

	CreateCertificate(context.Context, *model.Certificate) error
	ProvisionManagedCertificate(context.Context, *model.Certificate, string, time.Time) (int64, error)
	InstallCustomCertificate(context.Context, *model.Certificate, []string, time.Time) error
	ListCertificates(context.Context, Page) ([]model.Certificate, error)
	GetCertificate(context.Context, string) (model.Certificate, error)
	UpdateCertificate(context.Context, *model.Certificate, int64) error
	ActivateCertificate(context.Context, *model.Certificate, int64, time.Time) error
	SetCertificateDistributionRevision(context.Context, string, int64) error
	ListPendingCertificates(context.Context, time.Time, int) ([]model.Certificate, error)
	LinkCertificateDomains(context.Context, string, []string) error
	UpdateCertificateDomains(context.Context, string, model.DomainStatus, string, time.Time) error

	UpsertGateway(context.Context, *model.Gateway) error
	ListGateways(context.Context, Page) ([]model.Gateway, error)
	ListGatewayDNSTargets(context.Context, string) ([]model.DNSTarget, error)
	GetGateway(context.Context, string) (model.Gateway, error)
	UpdateGateway(context.Context, *model.Gateway, int64) error
	HeartbeatGateway(context.Context, string, int64, int64, time.Time) error
	AckGateway(context.Context, string, int64, string, string, string, string) error
	SelectProbeLeader(context.Context, string, time.Duration) (model.GatewayPool, error)
	GatewayACKed(context.Context, string, int64, time.Time) (bool, error)

	PutChallenge(context.Context, Challenge) error
	GetChallenge(context.Context, string, time.Time) (Challenge, error)
	DeleteChallenges(context.Context, string) error
	DeleteChallengeToken(context.Context, string, string) error
	GetACMEAccount(context.Context, string, string) (model.ACMEAccount, error)
	UpsertACMEAccount(context.Context, *model.ACMEAccount) error

	BuildSnapshot(context.Context, string) (model.Snapshot, error)
	RecordSnapshot(context.Context, string, int64, string, []byte, time.Time) error
	AppendAuditEvent(context.Context, model.AuditEvent) error
	ListAuditEvents(context.Context, Page) ([]model.AuditEvent, error)
	RecordRuntimeEvents(context.Context, model.RuntimeEventSource, []model.DiscoverySignal, time.Time) error
	MarkRuntimeEventSource(context.Context, model.RuntimeEventSource, string, time.Time) error
	ListRuntimeEventSources(context.Context) ([]model.RuntimeEventSource, error)
	ListTimeline(context.Context, TimelineFilter, Page) ([]model.TimelineRecord, error)
	PruneRuntimeEvents(context.Context, time.Time) error
	BuildProbePlan(context.Context, string) (model.ProbePlan, error)
	ReencryptCertificates(context.Context, func(model.Certificate) ([]byte, string, error)) (int, error)
	ReencryptACMEAccounts(context.Context, func(model.ACMEAccount) ([]byte, string, error)) (int, error)
	ReencryptApplicationRevisions(context.Context, func(model.ApplicationRevision) ([]byte, []byte, []byte, string, error)) (int, error)
	ReencryptDeploymentOperations(context.Context, func(model.DeploymentOperation) ([]byte, string, error)) (int, error)
	GarbageCollect(context.Context, time.Time, time.Duration) (GCResult, error)
	ReadOperationalMetrics(context.Context, time.Time) (OperationalMetrics, error)
}

type GCResult struct {
	ExpiredChallenges int64
	Certificates      int64
}

type ServiceFilter struct {
	// ExcludeRemoved is opt-in so internal ownership and recovery retain history.
	ExcludeRemoved bool
	GroupKey       string
	RuntimeID      string
	ApplicationID  string
	Status         model.ServiceStatus
	Managed        *bool
	Search         string
}

// OperationalMetrics is an aggregate, low-cardinality view of persisted
// controller state. Keeping these queries in the storage layer avoids an N+1
// metrics scrape path over services and endpoints.
type OperationalMetrics struct {
	Services             int
	InvalidServices      int
	Endpoints            int
	ReadyEndpoints       int
	RuntimeOnline        map[model.RuntimeType]int
	PoolDesired          map[string]int64
	PoolApplied          map[string]int64
	PoolProbeDesired     map[string]int64
	PoolProbeApplied     map[string]int64
	DNSStatuses          map[model.DNSStatus]int
	Certificates         map[model.CertificateStatus]int
	CertificateExpiry    map[string]float64
	DeploymentOperations map[model.DeploymentOperationStatus]int
	CatalogSources       map[string]int
}

type Challenge struct {
	ID               string
	CertificateID    string
	Domain           string
	Type             string
	Token            string
	KeyAuthorization string
	Status           string
	ExpiresAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type SQLStore struct {
	db             *sql.DB
	dialect        Dialect
	dsn            string
	executionMu    sync.RWMutex
	executionNodes map[string]model.RuntimeExecutionIdentity
}

func Open(driver, dsn string) (*SQLStore, error) {
	var sqlDriver string
	dialect := Dialect(driver)
	switch dialect {
	case Postgres:
		sqlDriver = "pgx"
	case MySQL:
		sqlDriver = "mysql"
	case SQLite:
		sqlDriver = "sqlite"
	default:
		return nil, fmt.Errorf("unsupported storage driver %q", driver)
	}
	db, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if dialect == SQLite {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(30)
		db.SetMaxIdleConns(10)
		db.SetConnMaxLifetime(30 * time.Minute)
	}
	return &SQLStore{db: db, dialect: dialect, dsn: dsn}, nil
}

func (s *SQLStore) DB() *sql.DB                       { return s.db }
func (s *SQLStore) Dialect() Dialect                  { return s.dialect }
func (s *SQLStore) Ping(ctx context.Context) error    { return s.db.PingContext(ctx) }
func (s *SQLStore) Close() error                      { return s.db.Close() }
func (s *SQLStore) Migrate(ctx context.Context) error { return migrate(ctx, s.db, s.dialect) }

func (s *SQLStore) bind(query string) string { return BindSQL(s.dialect, query) }

// BindSQL compiles named SQL markers to the placeholder syntax required by
// the selected driver. Each occurrence consumes one argument in SQL order;
// this also preserves existing calls that pass the same value more than once.
func BindSQL(dialect Dialect, query string) string {
	var out strings.Builder
	index := 1
	for i := 0; i < len(query); {
		if query[i] == '\'' || query[i] == '"' {
			quote := query[i]
			out.WriteByte(quote)
			i++
			for i < len(query) {
				out.WriteByte(query[i])
				if query[i] == quote {
					i++
					if i < len(query) && query[i] == quote {
						out.WriteByte(query[i])
						i++
						continue
					}
					break
				}
				i++
			}
			continue
		}
		if i+1 < len(query) && query[i] == '-' && query[i+1] == '-' {
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				out.WriteString(query[i:])
				break
			}
			out.WriteString(query[i : i+end])
			i += end
			continue
		}
		if i+1 < len(query) && query[i] == '/' && query[i+1] == '*' {
			end := strings.Index(query[i+2:], "*/")
			if end < 0 {
				out.WriteString(query[i:])
				break
			}
			end += i + 4
			out.WriteString(query[i:end])
			i = end
			continue
		}
		if query[i] == ':' && (i == 0 || query[i-1] != ':') && i+1 < len(query) && isSQLNameStart(query[i+1]) {
			end := i + 2
			for end < len(query) && isSQLNamePart(query[end]) {
				end++
			}
			if dialect == Postgres {
				fmt.Fprintf(&out, "$%d", index)
			} else {
				out.WriteByte('?')
			}
			index++
			i = end
			continue
		}
		if query[i] == '?' && dialect == Postgres {
			fmt.Fprintf(&out, "$%d", index)
			index++
		} else {
			out.WriteByte(query[i])
		}
		i++
	}
	return out.String()
}

func isSQLNameStart(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value == '_'
}

func isSQLNamePart(value byte) bool {
	return isSQLNameStart(value) || value >= '0' && value <= '9'
}

func mapError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique") || strings.Contains(message, "duplicate") || strings.Contains(message, "foreign key") {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func retryableTransactionError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "deadlock") || strings.Contains(message, "serialization failure") ||
		strings.Contains(message, "sqlstate 40001") || strings.Contains(message, "sqlstate 40p01") ||
		strings.Contains(message, "error 1213") || strings.Contains(message, "error 1205") ||
		strings.Contains(message, "database is locked")
}

// IsRetryableTransactionError lets callers restart the whole read/compute/write
// sequence after a database transaction was rolled back by a deadlock or
// serialization failure. Retrying only the final write would reuse stale input.
func IsRetryableTransactionError(err error) bool { return retryableTransactionError(err) }

func waitTransactionRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(10*(1<<attempt)) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func requireAffected(result sql.Result, expectedRevision bool) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if expectedRevision {
			return ErrPrecondition
		}
		return ErrNotFound
	}
	return nil
}

func columnPlaceholders(columns string) string {
	fields := strings.Split(columns, ",")
	for index, field := range fields {
		fields[index] = ":" + strings.TrimSpace(field)
	}
	return strings.Join(fields, ",")
}

func namedPlaceholders(prefix string, count int) string {
	if count <= 0 {
		return ""
	}
	var out strings.Builder
	for index := range count {
		if index > 0 {
			out.WriteByte(',')
		}
		fmt.Fprintf(&out, ":%s_%d", prefix, index+1)
	}
	return out.String()
}

func stringPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func intPtr(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolPtr(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}
