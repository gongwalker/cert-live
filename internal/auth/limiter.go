package auth

import (
	"sync"
	"time"
)

// 反暴力破解阈值（经验值）：
//   - 10 分钟窗口内累计 5 次密码错误 → 锁 15 分钟
//   - 只统计密码校验失败,不统计验证码失败(避免误锁)
const (
	loginWindow      = 10 * time.Minute
	loginLockout     = 15 * time.Minute
	loginMaxFails    = 5
	loginCleanupTick = 5 * time.Minute
	// 清理时:条目超过此存活时长且当前未锁定 → 删除
	loginEntryTTL = loginWindow + loginLockout
)

// ipState 记录单个 IP 在窗口内的失败状态
type ipState struct {
	failures    int       // 窗口内累计失败次数
	firstFail   time.Time // 窗口内首次失败时间,用于判断窗口是否过期
	lockedUntil time.Time // 锁定截止时间;time.Time 零值表示未锁
}

// LoginLimiter 按 IP 维度的登录失败限流器,全内存、无持久化
//
// 部署注意:c.ClientIP() 默认信任 X-Forwarded-For。
// 若服务直接暴露在公网(未经过 nginx/CDN),攻击者可伪造该 header 绕过限流。
// 生产环境建议用 nginx 反代,并通过 engine.SetTrustedProxies() 限制信任来源。
type LoginLimiter struct {
	mu     sync.Mutex
	states map[string]*ipState
}

// NewLoginLimiter 构造一个空的限流器
func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{states: make(map[string]*ipState)}
}

// Allow 返回该 IP 当前是否可尝试;若被锁,返回还需等待的时长
func (l *LoginLimiter) Allow(ip string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.states[ip]
	if st == nil {
		return true, 0
	}
	if now.Before(st.lockedUntil) {
		return false, st.lockedUntil.Sub(now)
	}
	return true, 0
}

// RecordFailure 记录一次失败,达阈值则锁定
func (l *LoginLimiter) RecordFailure(ip string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.states[ip]
	if st == nil {
		st = &ipState{firstFail: now}
		l.states[ip] = st
	}
	// 窗口已过期 → 重置窗口
	if !st.firstFail.IsZero() && now.Sub(st.firstFail) > loginWindow {
		st.failures = 0
		st.firstFail = now
	}
	st.failures++
	if st.failures >= loginMaxFails {
		st.lockedUntil = now.Add(loginLockout)
	}
}

// RecordSuccess 清除该 IP 的失败记录
func (l *LoginLimiter) RecordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.states, ip)
}

// StartCleanup 启动后台 goroutine 定期清陈旧条目,防止 map 无限增长
func (l *LoginLimiter) StartCleanup() {
	go func() {
		t := time.NewTicker(loginCleanupTick)
		defer t.Stop()
		for range t.C {
			l.cleanup()
		}
	}()
}

func (l *LoginLimiter) cleanup() {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, st := range l.states {
		// 仍在锁定窗口内 → 保留
		if now.Before(st.lockedUntil) {
			continue
		}
		// 已锁定但解锁了 + 窗口已过期 → 删除
		// 未锁且窗口过期超过 TTL → 删除
		if now.Sub(st.firstFail) > loginEntryTTL {
			delete(l.states, ip)
		}
	}
}
