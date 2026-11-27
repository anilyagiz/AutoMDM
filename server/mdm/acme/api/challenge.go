package api

import (
	"context"

	"github.com/anilyagiz/automdm/v4/server/mdm/acme/internal/types"
)

type ChallengeService interface {
	ValidateChallenge(ctx context.Context, enrollment *types.Enrollment, account *types.Account, challengeID uint, payload string) (*types.ChallengeResponse, error)
}
