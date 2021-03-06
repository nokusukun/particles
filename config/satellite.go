package config

type Satellite struct {
	Host        string
	Port        uint
	DisableUPNP bool
}

type Daemon struct {
	DialTo          string
	ApiListen       string
	KeyPath         string
	GenerateNewKeys bool
	ShowHelp        bool
	DatabasePath    string
}
