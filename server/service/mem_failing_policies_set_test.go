package service

import (
	"testing"

	"github.com/anilyagiz/automdm/v4/server/automdm/policytest"
)

func TestMemFailingPolicySet(t *testing.T) {
	m := NewMemFailingPolicySet()
	policytest.RunFailing1000hosts(t, m)
	m = NewMemFailingPolicySet()
	policytest.RunFailingBasic(t, m)
}
