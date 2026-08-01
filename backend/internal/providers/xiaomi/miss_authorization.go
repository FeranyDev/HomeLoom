package xiaomi

import "context"

// MISSAuthorizationResult is the non-account media authorization returned for
// one Xiaomi camera handshake. The account session remains encapsulated by
// this package; Camera Provider receives only the short-lived camera result.
type MISSAuthorizationResult struct {
	DevicePublic string
	Sign         string
	Vendor       string
	UID          string
}

// AcquireMISSCameraAuthorization uses an already established MIoT session
// directly. It deliberately does not initialize a CloudProvider or start a
// device polling lifecycle.
func AcquireMISSCameraAuthorization(ctx context.Context, config CloudConfig, did, clientPublic string) (MISSAuthorizationResult, error) {
	result, err := newHTTPMiotCloudClient(config).AcquireMISSAuthorization(ctx, did, clientPublic)
	if err != nil {
		return MISSAuthorizationResult{}, err
	}
	return MISSAuthorizationResult(result), nil
}
