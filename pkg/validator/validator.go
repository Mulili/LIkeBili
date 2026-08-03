package validator

import (
	"fmt"
	"regexp"

	vt "github.com/go-playground/validator/v10"
)

var (
	reUsername = regexp.MustCompile(`^[a-zA-Z0-9_]{3,20}$`) // 用户名：3-20 位字母、数字或下划线
	reLetter   = regexp.MustCompile(`[a-zA-Z]`)             // 密码需包含字母
	reDigit    = regexp.MustCompile(`[0-9]`)                // 密码需包含数字
)

var Validate *vt.Validate

func Init() error {
	Validate = vt.New()

	if err := Validate.RegisterValidation("username", validateUsername); err != nil {
		return fmt.Errorf("init validator failed: %v", err)
	}
	if err := Validate.RegisterValidation("username", validatePassword); err != nil {
		return fmt.Errorf("init validator failed: %v", err)
	}
	return nil
}

// Struct 对结构体执行所有已注册的校验规则，Handler 显式调用入口。
func Struct(s any) error {
	return Validate.Struct(s)
}

// validateUsername 校验用户名：3-20 位字母、数字或下划线。
func validateUsername(fl vt.FieldLevel) bool {
	return reUsername.MatchString(fl.Field().String())
}

// validatePassword 校验密码：至少 8 位，且必须同时包含字母和数字。
func validatePassword(fl vt.FieldLevel) bool {
	s := fl.Field().String()
	if len(s) < 8 {
		return false
	}
	return reLetter.MatchString(s) && reDigit.MatchString(s)
}
