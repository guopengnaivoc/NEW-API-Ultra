package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type Setup struct {
	Status       bool   `json:"status"`
	RootInit     bool   `json:"root_init"`
	DatabaseType string `json:"database_type"`
}

type SetupRequest struct {
	Username           string `json:"username"`
	Password           string `json:"password"`
	ConfirmPassword    string `json:"confirmPassword"`
	SelfUseModeEnabled bool   `json:"SelfUseModeEnabled"`
	DemoSiteEnabled    bool   `json:"DemoSiteEnabled"`
}

var loadSetupCompleted = constant.SetupCompleted

func currentSetupStatus() Setup {
	completed := loadSetupCompleted()
	setup := Setup{
		Status: completed,
	}
	if completed {
		return setup
	}
	setup.RootInit = model.RootUserExists()
	setup.DatabaseType = string(common.MainDatabaseType())
	return setup
}

func GetSetup(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    currentSetupStatus(),
	})
}

func PostSetup(c *gin.Context) {
	// Check if setup is already completed
	if constant.SetupCompleted() {
		c.JSON(200, gin.H{
			"success": false,
			"message": "系统已经初始化完成",
		})
		return
	}

	var req SetupRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": "请求参数有误",
		})
		return
	}

	// Validate username length: max 12 characters to align with model.User validation
	if len(req.Username) > 12 {
		c.JSON(200, gin.H{
			"success": false,
			"message": "用户名长度不能超过12个字符",
		})
		return
	}
	// Validate password
	if req.Password != req.ConfirmPassword {
		c.JSON(200, gin.H{
			"success": false,
			"message": "两次输入的密码不一致",
		})
		return
	}

	if len(req.Password) < 8 {
		c.JSON(200, gin.H{
			"success": false,
			"message": "密码长度至少为8个字符",
		})
		return
	}

	hashedPassword, err := common.Password2Hash(req.Password)
	if err != nil {
		common.SysError("failed to hash setup password: " + err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "系统初始化失败",
		})
		return
	}
	params := model.SetupInitializationParams{
		RootUser: model.User{
			Username:    req.Username,
			Password:    hashedPassword,
			Role:        common.RoleRootUser,
			Status:      common.UserStatusEnabled,
			DisplayName: "Root User",
			AccessToken: nil,
			Quota:       100000000,
		},
		SelfUseModeEnabled: req.SelfUseModeEnabled,
		DemoSiteEnabled:    req.DemoSiteEnabled,
	}
	if err := model.InitializeSystem(params); err != nil {
		if errors.Is(err, model.ErrSystemAlreadyInitialized) {
			if err := model.PublishPersistedSetupOptions(); err != nil {
				common.SysError(
					"failed to publish persisted setup options; local setup options may remain stale until the next successful option sync: " +
						err.Error(),
				)
			}
			constant.SetSetupCompleted(true)
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "系统已经初始化完成",
			})
			return
		}
		common.SysError("system initialization failed: " + err.Error())
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "系统初始化失败",
		})
		return
	}
	if err := model.PublishOptionValues(params.OptionValues()); err != nil {
		common.SysError("failed to publish committed setup options: " + err.Error())
	}
	constant.SetSetupCompleted(true)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "系统初始化成功",
	})
}
