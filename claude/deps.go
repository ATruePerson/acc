package claude

// Runtime hooks wired from the main package for bench and other CLI paths.
var (
	LoadDotenv        func(string)
	DefaultEnvPath    func() string
	LoadConfig        func(string) (*Config, error)
	DefaultConfigPath func() string
	RandID            func() string
	Truncate          func(string, int) string
)
