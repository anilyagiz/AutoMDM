package testpackage

import (
	"crypto" // for test
	"fmt"

	"github.com/anilyagiz/automdm/v4/server/archtest/test_files/dependency"
)

func What(_ crypto.Decrypter) {
	fmt.Println(dependency.Item)
}
