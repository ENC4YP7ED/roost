package store

// Record structs mirror the Pterodactyl schema. Nullable columns use
// pointers; JSON-typed columns are kept as raw strings and decoded at the
// API boundary.

type User struct {
	ID                  int64
	ExternalID          *string
	UUID                string
	Username            string
	Email               string
	NameFirst           string
	NameLast            string
	Password            string
	Language            string
	RootAdmin           bool
	UseTOTP             bool
	TOTPSecret          *string
	TOTPAuthenticatedAt *string
	CreatedAt           string
	UpdatedAt           string
}

type SSHKey struct {
	ID          int64
	UserID      int64
	Name        string
	Fingerprint string
	PublicKey   string
	CreatedAt   string
}

const (
	KeyTypeAccount     = 1
	KeyTypeApplication = 2
)

type APIKey struct {
	ID         int64
	UserID     int64
	KeyType    int
	Identifier string
	TokenHash  string
	Memo       string
	AllowedIPs string
	LastUsedAt *string
	CreatedAt  string
}

type Session struct {
	TokenHash string
	UserID    int64
	IP        string
	UserAgent string
	ExpiresAt string
	CreatedAt string
}

type Location struct {
	ID        int64
	Short     string
	Long      string
	CreatedAt string
	UpdatedAt string
}

type Node struct {
	ID                 int64
	UUID               string
	Public             bool
	Name               string
	Description        string
	LocationID         int64
	FQDN               string
	Scheme             string
	BehindProxy        bool
	MaintenanceMode    bool
	Memory             int64
	MemoryOverallocate int64
	Disk               int64
	DiskOverallocate   int64
	UploadSize         int64
	DaemonTokenID      string
	DaemonToken        string
	DaemonListen       int
	DaemonSFTP         int
	DaemonBase         string
	CreatedAt          string
	UpdatedAt          string
}

type Allocation struct {
	ID       int64
	NodeID   int64
	IP       string
	IPAlias  *string
	Port     int
	ServerID *int64
	Notes    *string
}

type Nest struct {
	ID          int64
	UUID        string
	Author      string
	Name        string
	Description string
	CreatedAt   string
	UpdatedAt   string
}

type Egg struct {
	ID               int64
	UUID             string
	NestID           int64
	Author           string
	Name             string
	Description      string
	Features         string // JSON array
	DockerImages     string // JSON object name -> image
	FileDenylist     string // JSON array
	UpdateURL        *string
	ConfigFiles      string
	ConfigStartup    string
	ConfigLogs       string
	ConfigStop       string
	ConfigFrom       *int64
	Startup          string
	ScriptContainer  string
	ScriptEntry      string
	ScriptPrivileged bool
	ScriptInstall    string
	CopyScriptFrom   *int64
	CreatedAt        string
	UpdatedAt        string
}

type EggVariable struct {
	ID           int64
	EggID        int64
	Name         string
	Description  string
	EnvVariable  string
	DefaultValue string
	UserViewable bool
	UserEditable bool
	Rules        string
	CreatedAt    string
	UpdatedAt    string
}

type Server struct {
	ID              int64
	ExternalID      *string
	UUID            string
	UUIDShort       string
	NodeID          int64
	Name            string
	Description     string
	Status          *string
	SkipScripts     bool
	OwnerID         int64
	Memory          int64
	Swap            int64
	Disk            int64
	IO              int64
	CPU             int64
	Threads         *string
	OOMDisabled     bool
	AllocationID    *int64
	NestID          int64
	EggID           int64
	Startup         string
	Image           string
	AllocationLimit int64
	DatabaseLimit   int64
	BackupLimit     int64
	InstalledAt     *string
	CreatedAt       string
	UpdatedAt       string
}

type ServerVariable struct {
	ID         int64
	ServerID   int64
	VariableID int64
	Value      string
}

type Subuser struct {
	ID          int64
	UserID      int64
	ServerID    int64
	Permissions string // JSON array
	CreatedAt   string
	UpdatedAt   string
}

type DatabaseHost struct {
	ID           int64
	Name         string
	Host         string
	Port         int
	Username     string
	Password     string
	MaxDatabases *int64
	NodeID       *int64
	CreatedAt    string
	UpdatedAt    string
}

type ServerDatabase struct {
	ID             int64
	ServerID       int64
	DatabaseHostID int64
	Database       string
	Username       string
	Remote         string
	Password       string
	MaxConnections int64
	CreatedAt      string
	UpdatedAt      string
}

type Schedule struct {
	ID             int64
	ServerID       int64
	Name           string
	CronDayOfWeek  string
	CronMonth      string
	CronDayOfMonth string
	CronHour       string
	CronMinute     string
	IsActive       bool
	IsProcessing   bool
	OnlyWhenOnline bool
	LastRunAt      *string
	NextRunAt      *string
	CreatedAt      string
	UpdatedAt      string
}

type ScheduleTask struct {
	ID                int64
	ScheduleID        int64
	SequenceID        int64
	Action            string
	Payload           string
	TimeOffset        int64
	IsQueued          bool
	ContinueOnFailure bool
	CreatedAt         string
	UpdatedAt         string
}

type Backup struct {
	ID           int64
	ServerID     int64
	UUID         string
	UploadID     *string
	IsSuccessful bool
	IsLocked     bool
	Name         string
	IgnoredFiles string // JSON array
	Disk         string
	Checksum     *string
	Bytes        int64
	CompletedAt  *string
	DeletedAt    *string
	CreatedAt    string
	UpdatedAt    string
}

type Mount struct {
	ID            int64
	UUID          string
	Name          string
	Description   string
	Source        string
	Target        string
	ReadOnly      bool
	UserMountable bool
}

type ActivityLog struct {
	ID          int64
	Batch       *string
	Event       string
	IP          string
	Description *string
	ActorID     *int64
	APIKeyID    *int64
	Properties  string // JSON object
	Timestamp   string
}
