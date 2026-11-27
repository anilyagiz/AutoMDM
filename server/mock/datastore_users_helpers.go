package mock

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/automdm"
)

func UserByEmailWithUser(u *automdm.User) UserByEmailFunc {
	return func(ctx context.Context, email string) (*automdm.User, error) {
		return u, nil
	}
}

func UserWithEmailNotFound() UserByEmailFunc {
	return func(ctx context.Context, email string) (*automdm.User, error) {
		return nil, &Error{"not found"}
	}
}

func UserWithID(u *automdm.User) UserByIDFunc {
	return func(ctx context.Context, id uint) (*automdm.User, error) {
		return u, nil
	}
}
