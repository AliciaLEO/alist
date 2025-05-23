package teldrive

import (
	"context"
	"time"

	"github.com/AliciaLEO/alist-pro/v3/internal/model"
	"github.com/AliciaLEO/alist-pro/v3/pkg/utils"
)

// Object TelDrive对象结构体
type Object struct {
	ID       string
	Name     string
	Size     int64
	ModTime  time.Time
	IsFolder bool
	Path     string
	ParentID string
	driver   *TelDrive
}

// GetSize 获取文件大小
func (o *Object) GetSize() int64 {
	return o.Size
}

// GetName 获取文件名称
func (o *Object) GetName() string {
	return o.Name
}

// ModTime 获取修改时间
func (o *Object) ModTime() time.Time {
	return o.ModTime
}

// CreateTime 获取创建时间（TelDrive API不提供创建时间，使用修改时间代替）
func (o *Object) CreateTime() time.Time {
	return o.ModTime
}

// IsDir 是否是目录
func (o *Object) IsDir() bool {
	return o.IsFolder
}

// GetHash 获取文件哈希（TelDrive不提供文件哈希）
func (o *Object) GetHash() utils.HashInfo {
	return utils.HashInfo{}
}

// GetID 获取文件ID
func (o *Object) GetID() string {
	return o.ID
}

// GetPath 获取文件路径
func (o *Object) GetPath() string {
	return o.Path
}

// GetRoot 获取根目录对象
func (d *TelDrive) GetRoot(ctx context.Context) (model.Obj, error) {
	return &Object{
		ID:       "root",
		Name:     "",
		Size:     0,
		ModTime:  time.Now(),
		IsFolder: true,
		Path:     "/",
		driver:   d,
	}, nil
}

var _ model.Obj = (*Object)(nil)