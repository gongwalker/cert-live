package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

const cookieName = "certlive_token"

// 运行期密钥（由 Configure 注入）
var secretKey = "certlive-demo-secret-2026"

// Configure 在 main 启动时注入 cookie 签名密钥
func Configure(secret string) {
	if secret != "" {
		secretKey = secret
	}
}

// HashPassword 使用 bcrypt 生成密码哈希（管理员入库时用）
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验 bcrypt 密码哈希
func CheckPassword(hash, pw string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}

// sign 用 HMAC-SHA256 对用户名签名
func sign(user string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(user))
	return hex.EncodeToString(mac.Sum(nil))
}

// SetLogin 登录成功后下发签名 cookie
func SetLogin(c *gin.Context, user string) {
	value := user + "." + sign(user)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// Logout 清除 cookie
func Logout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

// CurrentUser 从 cookie 取已登录用户名，未登录或签名错误返回空串
func CurrentUser(c *gin.Context) string {
	cookie, err := c.Cookie(cookieName)
	if err != nil {
		return ""
	}
	parts := strings.SplitN(cookie, ".", 2)
	if len(parts) != 2 {
		return ""
	}
	user, sig := parts[0], parts[1]
	if !hmac.Equal([]byte(sign(user)), []byte(sig)) {
		return ""
	}
	return user
}

// RequireAuth 中间件：未登录重定向到 /login（页面）或 401（接口）
func RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if CurrentUser(c) == "" {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "未登录"})
				return
			}
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}