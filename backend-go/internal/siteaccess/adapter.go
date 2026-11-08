package siteaccess

import (
	"context"
	"errors"
)

var ErrGroupModelsUnknown = errors.New("尚无可查询该分组的推理 Key，请选择模型后接入并验证")

type siteAdapter interface {
	FetchGroups(context.Context, string, string, string, string) (map[string]float64, error)
	FetchGroupModels(context.Context, string, string, string, string, string) ([]string, error)
	FetchBalance(context.Context, string, string, string, string) (float64, string, error)
	VerifyWithFallback(context.Context, string, string, string, string) (*NewApiUserSelf, string, error)
	ListTokens(context.Context, string, string, string, string, int, int) ([]NewApiToken, error)
	ListAllTokens(context.Context, SiteAccount, string) ([]NewApiToken, error)
	ProvisionKey(context.Context, string, string, string, string, NewApiProvisionOptions) (int, string, bool, string, error)
	GetTokenKey(context.Context, string, string, string, string, int) (string, error)
	DeleteToken(context.Context, string, string, string, string, int) error
}

func adapterForAccount(account SiteAccount, base *NewApiAdapter) siteAdapter {
	if account.SiteType == "sub2api" {
		return &sub2APIAdapter{base: base}
	}
	return base
}
