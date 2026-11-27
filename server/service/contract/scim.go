package contract

import "github.com/anilyagiz/automdm/v4/server/automdm"

type ScimDetailsResponse struct {
	automdm.ScimDetails
	Err error `json:"-"`
}

func (r ScimDetailsResponse) Error() error {
	return r.Err
}
