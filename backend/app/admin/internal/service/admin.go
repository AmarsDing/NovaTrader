package service

import "context"

// AdminService 是裁掉模板登录之后留下的占位。
// middleware 仍调用 GetMenuid，避免整包鉴权代码先删掉。
type AdminService struct{}

func NewAdminService() *AdminService {
	return &AdminService{}
}

func (admin *AdminService) GetMenuid(ctx context.Context, path string) string {
	return path
}
