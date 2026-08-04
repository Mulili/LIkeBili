// Package validator 封装基于 validator/v10 的参数校验与中文错误翻译。
//
// 设计说明：
//   - 本项目采用 Handler 显式调用方式：Handler 里先 ShouldBindJSON，再调用
//     本包的 Struct 执行校验（不替换 gin 默认校验器）。
//   - 校验规则写在 DTO 的 validate tag 上（如 `validate:"required,username"`），
//     自定义规则（username/password）由本包在 Init 中注册。
//   - 校验失败的错误通过 TranslateError 翻译成中文后再返回给前端。
package validator

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/go-playground/locales/zh"                            // 中文语言包：内置规则的翻译文本
	ut "github.com/go-playground/universal-translator"               // 翻译器：维护 "tag → 模板字符串" 的映射
	vt "github.com/go-playground/validator/v10"                      // 校验器本体
	zhTrans "github.com/go-playground/validator/v10/translations/zh" // 内置规则中文翻译的批量注册器
)

// 预编译正则：包初始化时编译一次，之后直接复用。
// 相比每次校验都 regexp.MatchString（内部每次都会重新编译正则），性能更好。
var (
	reUsername = regexp.MustCompile(`^[a-zA-Z0-9_]{3,20}$`) // 用户名：3-20 位字母、数字或下划线
	reLetter   = regexp.MustCompile(`[a-zA-Z]`)             // 密码需包含字母
	reDigit    = regexp.MustCompile(`[0-9]`)                // 密码需包含数字
)

var (
	// Validate 全局校验器实例，由 Init 初始化。
	// 校验规则、翻译函数、字段名规则都注册在它身上。
	Validate *vt.Validate

	// trans 全局翻译器，由 Init 初始化。
	// 注意：ut.Translator 本身是接口，不能写成 *ut.Translator。
	trans ut.Translator
)

// Init 初始化校验器和翻译器，注册路由前必须调用一次。
// 返回 error：任何一步注册失败都会返回，由调用方决定如何处理（如 log.Fatal）。
func Init() error {
	// 创建校验器实例。为什么不直接用全局单例？
	// 因为我们要在它上面做自定义配置（注册规则、翻译、字段名规则），
	// 所以自己 new 一个，把它当成"我们的配置中心"。
	Validate = vt.New()

	// ---------- 1. 注册自定义校验规则 ----------
	// RegisterValidation(tag名, 校验函数)：
	//   参数① "username" —— DTO 里 validate tag 写的名字，如 `validate:"username"`。
	//             校验器解析 tag 时，会拿这个名字去内部 map（validations）查函数。
	//             所以这里填什么，DTO 里就必须写什么，一个字符都不能差。
	//   参数② validateUsername —— 校验函数，校验器遇到 "username" 这个 tag 时就回调它。
	//             函数签名固定为 func(fl vt.FieldLevel) bool，返回 true 表示通过。
	if err := Validate.RegisterValidation("username", validateUsername); err != nil {
		return fmt.Errorf("init validator failed: %v", err)
	}
	if err := Validate.RegisterValidation("password", validatePassword); err != nil {
		return fmt.Errorf("init validator failed: %v", err)
	}

	// ---------- 2. 初始化中文翻译器 ----------
	zhT := zh.New() // 创建中文语言包：内置规则的翻译文本（如 required → "{0}为必填字段"）都存放在里面
	// ut.New(回退语言, 支持的语言...)：第一个参数是找不到翻译时的兜底语言，
	// 第二个及以后是支持的语言列表。这里只有中文，所以两个都是 zhT。
	uni := ut.New(zhT, zhT)
	// GetTranslator("zh")：按语言代码取出翻译器，返回 (Translator, error)。
	// 用 = 赋值给【包级】trans；如果误写成 := 会创建局部变量遮蔽全局，
	// 导致下面 TranslateError 里用的 trans 是 nil 而 panic。
	trans, _ = uni.GetTranslator("zh")

	// ---------- 3. 内置规则自动翻译 ----------
	// RegisterDefaultTranslations(校验器, 翻译器)：
	//   参数① Validate —— 翻译函数要注册到哪个校验器上（因为我们之后用 Validate.Struct 校验）
	//   参数② trans —— 翻译函数取文本时用哪个翻译器
	// 作用：把中文语言包里所有内置规则（required/email/min/max...）的翻译文本批量注册进校验器。
	// 本质是：给每个内置 tag 注册一个"翻译函数"。
	// 注意：这里只覆盖内置规则，自定义规则（username/password）需要第 4 步单独注册。
	if err := zhTrans.RegisterDefaultTranslations(Validate, trans); err != nil {
		return fmt.Errorf("register default translations failed: %v", err)
	}

	// ---------- 4. 自定义规则翻译 ----------
	// 机制与内置规则完全相同，只是要手动逐个注册。
	// RegisterTranslation(tag, trans, 注册函数, 取消息函数) 四个参数：
	//   ① "username" —— 翻译哪个 tag，必须与第 1 步 RegisterValidation 的名字一致
	//   ② trans —— 翻译器，注册函数往里存文本、取消息函数从里取文本
	//   ③ 注册函数 func(ut.Translator) error —— 只执行一次（初始化时）：
	//      ut.Add(key, 文本, override)：往翻译器存一条模板文本。
	//        key  = "username"（存取的钥匙，与取消息时 ut.T 的第一个参数对应）
	//        文本  = "{0}必须为3-20位字母、数字或下划线"，{0} 是字段名的占位符
	//        override = true 表示已有同名 key 也覆盖写入（重跑 Init 不报错）
	//   ④ 取消息函数 func(ut, fe) string —— 每次校验失败时被调用：
	//      fe.Field() 返回"字段显示名"（受第 5 步 RegisterTagNameFunc 影响，这里是 username）
	//      ut.T(key, 参数...) 把模板里的 {0} 替换成参数，返回最终中文文本
	if err := Validate.RegisterTranslation("username", trans, func(ut ut.Translator) error {
		return ut.Add("username", "{0}必须为3-20位字母、数字或下划线", true)
	},
		func(ut ut.Translator, fe vt.FieldError) string {
			t, _ := ut.T("username", fe.Field())
			return t
		}); err != nil {
		return err
	}
	if err := Validate.RegisterTranslation("password", trans,
		func(ut ut.Translator) error {
			return ut.Add("password", "{0}必须至少8位，且同时包含字母和数字", true)
		},
		func(ut ut.Translator, fe vt.FieldError) string {
			t, _ := ut.T("password", fe.Field())
			return t
		}); err != nil {
		return err
	}

	// ---------- 5. 字段显示名规则 ----------
	// RegisterTagNameFunc(函数)：注册"字段显示名"的生成规则。
	//   参数：func(fld reflect.StructField) string —— 入参是反射到的结构体字段，
	//         出参是该字段在错误消息里的显示名。
	// 为什么用 reflect.StructField？因为校验器是靠反射读取结构体字段及其 tag 的。
	// 默认情况下，显示名是 Go 字段名（如 Username、Password）；
	// 这里改为取 json tag 的名字（如 username、password），
	// 这样所有翻译消息里 {0} 替换出来的都是小写的 json 名，更贴近前端习惯。
	Validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0] // 取 json:"xxx" 里的 xxx，忽略后面的选项
		if name == "-" {                                       // json:"-" 表示不序列化，返回空名
			return ""
		}
		return name
	})
	return nil
}

// Struct 对结构体执行所有已注册的校验规则，Handler 显式调用入口。
// 参数 s：要校验的结构体，传入指针（&req）。
//
//	为什么传指针？① 是 validator 的约定，指针才能正确处理字段上的指针类型；
//	② 避免把结构体整体复制一份。
//
// 校验器通过反射读取 s 的类型信息和每个字段的 validate tag，逐个执行规则。
func Struct(s any) error {
	return Validate.Struct(s)
}

// TranslateError 把校验错误翻译成中文提示，供 Handler 直接塞进响应。
// 参数 err：Struct 或 ShouldBindJSON 返回的 error。
//
// 翻译流程（"自动翻译"的原理）：
//  1. Validate.Struct 校验失败时返回 validator.ValidationErrors，
//     它是一组 FieldError，每条记录一条失败信息：哪个字段、违反了哪个 tag；
//  2. verr[0].Translate(trans) 按该 FieldError 的 tag 在内部查找"翻译函数"：
//     内置规则由 RegisterDefaultTranslations 注册，自定义规则由 RegisterTranslation 注册；
//  3. 翻译函数调用 trans.T(tag, 字段名)，把模板里的 {0} 替换成字段名，得到中文结果。
//
// 为什么用 errors.As(err, &verr)？
//   - 校验失败时，err 的类型是 validator.ValidationErrors（它实现了 error 接口）；
//   - errors.As 会沿着错误链查找，找到匹配类型就把它赋值给 verr；
//   - 第二个参数必须传 &verr（目标类型的指针），这是 errors.As 的接口约定。
//
// 若 err 不是校验错误（如 JSON 解析失败），errors.As 找不到 ValidationErrors，
// 返回兜底提示"请求参数格式错误"。
func TranslateError(err error) string {
	var verr vt.ValidationErrors
	if errors.As(err, &verr) {
		return verr[0].Translate(trans)
	}
	return "请求参数格式错误"
}

// validateUsername 校验用户名：3-20 位字母、数字或下划线。
// 参数 fl vt.FieldLevel：校验器给我们的"当前字段上下文"，用来读取被校验字段的值。
//
//	为什么是 FieldLevel 而不是直接传 string？
//	因为校验器不知道字段是什么类型，所以统一通过反射给一个 reflect.Value：
//	fl.Field() 拿到被校验字段的 reflect.Value，.String() 取出字符串值。
//
// 返回值：true 表示通过，false 表示失败（失败时前端会收到第 4 步注册的翻译文本）。
func validateUsername(fl vt.FieldLevel) bool {
	return reUsername.MatchString(fl.Field().String())
}

// validatePassword 校验密码：至少 8 位，且必须同时包含字母和数字。
// 参数 fl 的含义同 validateUsername。
// 拆成三个独立条件：长度 >= 8、含字母、含数字，全部满足才算通过。
// 之所以用两个正则而不是一个：Go 的正则引擎（RE2）不支持 lookahead，
// 无法用一个正则表达"含字母且含数字"。
func validatePassword(fl vt.FieldLevel) bool {
	s := fl.Field().String()
	if len(s) < 8 {
		return false
	}
	return reLetter.MatchString(s) && reDigit.MatchString(s)
}
