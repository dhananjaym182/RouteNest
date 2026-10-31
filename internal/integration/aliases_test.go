package integration

import "cline-go-proxy/internal/domain"

type Account = domain.Account
type AccountPool = domain.AccountPool
type AttemptLog = domain.AttemptLog
type ComboEntry = domain.ComboEntry
type Connection = domain.Connection
type CustomProvider = domain.CustomProvider
type LoginMethod = domain.LoginMethod
type Model = domain.Model
type ModelCombo = domain.ModelCombo
type ModelStat = domain.ModelStat
type RequestLog = domain.RequestLog

const (
	MethodDeviceOAuth  = domain.MethodDeviceOAuth
	MethodRefreshToken = domain.MethodRefreshToken
	MethodSSOCookie    = domain.MethodSSOCookie
)
