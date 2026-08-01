package api

// HomeLoom never exposes the generic go2rtc Web UI or a caller-selected
// static directory. The API listener exists only for explicitly allow-listed
// HomeKit pairing and Device Center preview paths.
func initStatic(string) {}
