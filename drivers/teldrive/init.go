package teldrive

import (
	"github.com/AliciaLEO/alist-pro/v3/internal/driver"
	"github.com/AliciaLEO/alist-pro/v3/internal/op"
)

const Type = "TelDrive"

func init() {
	op.RegisterDriver(Type, func() driver.Driver {
		return &TelDrive{}
	})
}