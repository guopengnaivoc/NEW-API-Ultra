package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	secureVerificationMethod2FA      = "2fa"
	secureVerificationMethodPasskey  = "passkey"
	secureVerificationMethodPassword = "password"

	securityProofScopeEmailChange       = "email.change"
	securityProofScopeTwoFASetup        = "twofa.setup"
	securityProofScopeExternalBind      = "external.bind"
	securityProofScopeExternalUnbind    = "external.unbind"
	securityProofScopePATRotate         = "pat.rotate"
	securityProofScopeAccountDelete     = "account.delete"
	securityProofScopeTokenKeyRotate    = "token.key.rotate"
	securityProofScopeAdminBindingClear = "admin.binding.clear"
	securityProofScopeAdminTwoFADisable = "admin.twofa.disable"
	securityProofScopeAdminPasskeyReset = "admin.passkey.reset"
	maxSecurityProofTargetLength        = 512
)

type UniversalVerifyRequest struct {
	Method   string `json:"method"`
	Code     string `json:"code,omitempty"`
	Password string `json:"password,omitempty"`
	Scope    string `json:"scope"`
	Target   string `json:"target,omitempty"`
}

func UniversalVerify(c *gin.Context) {
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "当前认证方式不支持安全验证"})
		return
	}
	var request UniversalVerifyRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, fmt.Errorf("参数错误: %v", err))
		return
	}
	request.Method = strings.TrimSpace(request.Method)
	request.Scope = strings.TrimSpace(request.Scope)
	target, ok := normalizeSecurityProofTarget(request.Method, request.Scope, request.Target)
	if !ok {
		common.ApiError(c, errors.New("不支持的安全验证范围"))
		return
	}
	request.Target = target
	if request.Method == secureVerificationMethodPasskey {
		common.ApiError(c, errors.New("Passkey 验证必须使用 Passkey verify 流程"))
		return
	}
	switch request.Method {
	case secureVerificationMethod2FA:
		if strings.TrimSpace(request.Code) == "" {
			common.ApiError(c, errors.New("验证码不能为空"))
			return
		}
		twoFA, err := model.GetTwoFAByUserId(identity.UserID)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if twoFA == nil || !twoFA.IsEnabled {
			common.ApiError(c, errors.New("用户未启用2FA"))
			return
		}
		if !validateTwoFactorAuth(twoFA, request.Code) {
			common.ApiError(c, errors.New("验证失败，请检查验证码"))
			return
		}
	case secureVerificationMethodPassword:
		if request.Password == "" {
			common.ApiError(c, errors.New("密码不能为空"))
			return
		}
		user, err := model.GetUserById(identity.UserID, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if user.Password == "" || !common.ValidatePasswordAndHash(request.Password, user.Password) {
			common.ApiError(c, errors.New("密码验证失败"))
			return
		}
	default:
		common.ApiError(c, errors.New("不支持的安全验证方式"))
		return
	}
	proofToken, expiresAt, err := service.IssueOneTimeSecurityProof(
		identity,
		request.Method,
		request.Scope,
		request.Target,
	)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordLog(identity.UserID, model.LogTypeSystem, "通用安全验证成功 (验证方式: "+request.Method+")")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "验证成功",
		"data": gin.H{
			"proof_token": proofToken,
			"expires_at":  expiresAt,
			"method":      request.Method,
			"scope":       request.Scope,
			"target":      request.Target,
		},
	})
}

func normalizeSecurityProofTarget(method, scope, rawTarget string) (string, bool) {
	if len(rawTarget) > maxSecurityProofTargetLength {
		return "", false
	}
	if scope == securityProofScopeChannelKeyRead {
		if method != secureVerificationMethod2FA &&
			method != secureVerificationMethodPasskey {
			return "", false
		}
		target, err := service.CanonicalSecurityProofIDTarget(rawTarget)
		return target, err == nil
	}

	target := strings.TrimSpace(rawTarget)
	switch scope {
	case securityProofScopePasskeyRegister,
		securityProofScopePasskeyDelete,
		securityProofScopeEmailChange,
		securityProofScopeTwoFASetup,
		securityProofScopeExternalBind,
		securityProofScopeExternalUnbind,
		securityProofScopeAdminBindingClear,
		securityProofScopeAdminTwoFADisable,
		securityProofScopeAdminPasskeyReset:
		return target, target != ""
	case securityProofScopePATRotate, securityProofScopeAccountDelete:
		return target, target == "self"
	case securityProofScopeTokenKeyRotate:
		return target, target != ""
	default:
		return "", false
	}
}
