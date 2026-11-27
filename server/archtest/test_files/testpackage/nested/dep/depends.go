package dep

import (
	"fmt"

	"github.com/anilyagiz/automdm/v4/server/archtest/test_files/nesteddependency"
)

func init() {
	fmt.Println(nesteddependency.Item)
}
