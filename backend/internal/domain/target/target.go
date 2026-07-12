package target

type Config struct {
	ID        string
	Type      string
	Name      string
	Enabled   bool
	Address   string
	Pin       string
	SetupID   string
	StorePath string
	DeviceIDs []string
}
