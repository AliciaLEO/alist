package teldrive

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AliciaLEO/alist-pro/v3/internal/driver"
	"github.com/AliciaLEO/alist-pro/v3/internal/errs"
	"github.com/AliciaLEO/alist-pro/v3/internal/model"
	"github.com/AliciaLEO/alist-pro/v3/pkg/utils"
	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
)

type TelDrive struct {
	model.Storage
	Addition

	client *resty.Client
	userId int64
}

type Addition struct {
	AccessToken      string `json:"access_token" required:"true" help:"TelDrive访问令牌Cookie"`
	ApiHost          string `json:"api_host" required:"true" help:"TelDrive API主机地址"`
	UploadHost       string `json:"upload_host" help:"TelDrive上传API主机地址（可选）"`
	ChannelID        string `json:"channel_id" required:"true" help:"TelDrive频道ID"`
	ChunkSize        int64  `json:"chunk_size" default:"500" help:"分块大小(MB)，默认500MB"`
	RandomChunkName  bool   `json:"random_chunk_name" default:"true" help:"使用随机分块名称增强安全性"`
	EncryptFiles     bool   `json:"encrypt_files" default:"false" help:"启用TelDrive原生加密"`
	UploadConcurrency int    `json:"upload_concurrency" default:"4" help:"上传并发数"`
}

type FileInfo struct {
	Id       string    `json:"id"`
	Name     string    `json:"name"`
	MimeType string    `json:"mimeType"`
	Size     int64     `json:"size"`
	ParentId string    `json:"parentId"`
	Type     string    `json:"type"`
	ModTime  time.Time `json:"updatedAt"`
}

type Meta struct {
	Count       int `json:"count,omitempty"`
	TotalPages  int `json:"totalPages,omitempty"`
	CurrentPage int `json:"currentPage,omitempty"`
}

type ReadMetadataResponse struct {
	Files []FileInfo `json:"items"`
	Meta  Meta       `json:"meta"`
}

type PartFile struct {
	Name       string `json:"name"`
	PartId     int    `json:"partId"`
	PartNo     int    `json:"partNo"`
	TotalParts int    `json:"totalParts"`
	Size       int64  `json:"size"`
	ChannelID  int64  `json:"channelId"`
	Encrypted  bool   `json:"encrypted"`
	Salt       string `json:"salt"`
}

type FilePart struct {
	ID   int    `json:"id"`
	Salt string `json:"salt,omitempty"`
}

type CreateFileRequest struct {
	Name      string     `json:"name"`
	Type      string     `json:"type"`
	Path      string     `json:"path,omitempty"`
	MimeType  string     `json:"mimeType,omitempty"`
	Size      int64      `json:"size,omitempty"`
	ChannelID int64      `json:"channelId,omitempty"`
	Encrypted bool       `json:"encrypted,omitempty"`
	Parts     []FilePart `json:"parts,omitempty"`
	ParentId  string     `json:"parentId,omitempty"`
	ModTime   time.Time  `json:"updatedAt,omitempty"`
}

type Session struct {
	UserName string `json:"userName"`
	UserId   int64  `json:"userId"`
	Hash     string `json:"hash"`
}

type uploadInfo struct {
	existingChunks map[int]PartFile
	uploadID       string
	channelID      int64
	encryptFile    bool
	chunkSize      int64
	totalChunks    int64
	fileChunks     []FilePart
	fileName       string
	dir            string
}

var config = driver.Config{
	Name:        "TelDrive",
	DefaultRoot: "/",
}

func (d *TelDrive) Config() driver.Config {
	return config
}

func (d *TelDrive) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *TelDrive) Init(ctx context.Context) error {
	d.client = resty.New()
	d.client.SetBaseURL(d.ApiHost)
	d.client.SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36")
	d.client.SetCookie(&http.Cookie{
		Name:  "access_token",
		Value: d.AccessToken,
	})

	// 获取用户信息
	var session Session
	resp, err := d.client.R().
		SetResult(&session).
		Get("/api/auth/session")

	if err != nil {
		return err
	}

	if resp.StatusCode() != 200 {
		return fmt.Errorf("获取用户信息失败: %s", resp.String())
	}

	d.userId = session.UserId

	return nil
}

func (d *TelDrive) Drop(ctx context.Context) error {
	return nil
}

func getMD5Hash(text string) string {
	hash := md5.Sum([]byte(text))
	return hex.EncodeToString(hash[:])
}

func (d *TelDrive) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	path := dir.GetPath()
	if path == "/" {
		path = ""
	}

	var resp ReadMetadataResponse
	_, err := d.client.R().
		SetQueryParam("path", path).
		SetQueryParam("page", "1").
		SetQueryParam("limit", "1000").
		SetResult(&resp).
		Get("/api/files")

	if err != nil {
		return nil, err
	}

	var files []model.Obj
	for _, file := range resp.Files {
		obj := &Object{
			ID:       file.Id,
			Name:     file.Name,
			Size:     file.Size,
			ModTime:  file.ModTime,
			IsFolder: file.Type == "folder",
			Path:     path + "/" + file.Name,
			ParentID: file.ParentId,
			driver:   d,
		}
		files = append(files, obj)
	}

	return files, nil
}

func (d *TelDrive) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	obj, ok := file.(*Object)
	if !ok {
		return nil, fmt.Errorf("无效的对象类型")
	}

	if obj.IsFolder {
		return nil, errs.NotFile
	}

	var downloadURL string
	resp, err := d.client.R().
		SetQueryParam("id", obj.ID).
		Get("/api/files/download")

	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("获取下载链接失败: %s", resp.String())
	}

	downloadURL = resp.Header().Get("Location")
	if downloadURL == "" {
		return nil, fmt.Errorf("获取下载链接失败: 未找到重定向URL")
	}

	return &model.Link{
		URL: downloadURL,
		Header: http.Header{
			"User-Agent": {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36"},
		},
	}, nil
}

func (d *TelDrive) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	parentPath := parentDir.GetPath()
	if parentPath == "/" {
		parentPath = ""
	}

	newPath := path.Join(parentPath, dirName)

	resp, err := d.client.R().
		SetBody(map[string]string{
			"path": newPath,
		}).
		Post("/api/files/folder")

	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("创建文件夹失败: %s", resp.String())
	}

	// 获取新创建的文件夹信息
	var fileInfo FileInfo
	err = json.Unmarshal(resp.Body(), &fileInfo)
	if err != nil {
		return nil, err
	}

	return &Object{
		ID:       fileInfo.Id,
		Name:     dirName,
		Size:     0,
		ModTime:  fileInfo.ModTime,
		IsFolder: true,
		Path:     newPath,
		ParentID: fileInfo.ParentId,
		driver:   d,
	}, nil
}

func (d *TelDrive) Put(ctx context.Context, dstDir model.Obj, file model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	parentPath := dstDir.GetPath()
	if parentPath == "/" {
		parentPath = ""
	}

	fileName := file.GetName()
	fileSize := file.GetSize()

	if fileSize < 0 {
		return nil, fmt.Errorf("不支持未知大小的文件上传")
	}

	// 准备上传信息
	channelID, _ := strconv.ParseInt(d.ChannelID, 10, 64)
	chunkSize := d.ChunkSize * 1024 * 1024 // 转换为字节
	totalChunks := (fileSize + chunkSize - 1) / chunkSize

	// 生成上传ID
	uploadID := getMD5Hash(fmt.Sprintf("%s:%s:%d:%d", parentPath, fileName, fileSize, d.userId))

	// 检查是否有已存在的分块
	var existingChunks map[int]PartFile = make(map[int]PartFile)
	resp, err := d.client.R().Get("/api/uploads/" + uploadID)
	if err == nil && resp.StatusCode() == 200 {
		var parts []PartFile
		err = json.Unmarshal(resp.Body(), &parts)
		if err == nil {
			for _, part := range parts {
				existingChunks[part.PartNo] = part
			}
		}
	}

	// 上传文件分块
	var partsToCommit []PartFile
	var uploadedSize int64

	for chunkNo := 1; chunkNo <= int(totalChunks); chunkNo++ {
		if existing, ok := existingChunks[chunkNo]; ok {
			// 跳过已上传的分块
			io.CopyN(io.Discard, file, existing.Size)
			partsToCommit = append(partsToCommit, existing)
			uploadedSize += existing.Size
			up(float64(uploadedSize) / float64(fileSize) * 100)
			continue
		}

		n := chunkSize
		if chunkNo == int(totalChunks) {
			n = fileSize - uploadedSize
		}

		chunkName := fileName
		if d.RandomChunkName {
			chunkName = getMD5Hash(uuid.New().String())
		} else if totalChunks > 1 {
			chunkName = fmt.Sprintf("%s.part.%03d", fileName, chunkNo)
		}

		partReader := io.LimitReader(file, n)

		// 构建上传URL
		uploadURL := d.ApiHost + "/api/uploads/" + uploadID
		if d.UploadHost != "" {
			uploadURL = d.UploadHost + "/api/uploads/" + uploadID
		}

		// 构建查询参数
		params := url.Values{}
		params.Add("partName", chunkName)
		params.Add("fileName", fileName)
		params.Add("partNo", strconv.Itoa(chunkNo))
		params.Add("channelId", d.ChannelID)
		params.Add("encrypted", strconv.FormatBool(d.EncryptFiles))

		// 上传分块
		resp, err := d.client.R().
			SetQueryParamsFromValues(params).
			SetHeader("Content-Type", "application/octet-stream").
			SetBody(partReader).
			Post(uploadURL)

		if err != nil {
			return nil, fmt.Errorf("上传分块失败: %v", err)
		}

		if resp.StatusCode() != 200 {
			return nil, fmt.Errorf("上传分块失败: %s", resp.String())
		}

		// 解析分块信息
		var partInfo PartFile
		err = json.Unmarshal(resp.Body(), &partInfo)
		if err != nil {
			return nil, fmt.Errorf("解析分块信息失败: %v", err)
		}

		if partInfo.PartId == 0 {
			return nil, fmt.Errorf("上传分块失败: 未获取到分块ID")
		}

		uploadedSize += n
		partsToCommit = append(partsToCommit, partInfo)
		up(float64(uploadedSize) / float64(fileSize) * 100)
	}

	// 按分块序号排序
	sort.Slice(partsToCommit, func(i, j int) bool {
		return partsToCommit[i].PartNo < partsToCommit[j].PartNo
	})

	// 准备文件分块信息
	fileChunks := []FilePart{}
	for _, part := range partsToCommit {
		fileChunks = append(fileChunks, FilePart{ID: part.PartId, Salt: part.Salt})
	}

	// 创建文件
	createFileReq := CreateFileRequest{
		Name:      fileName,
		Type:      "file",
		Path:      path.Join(parentPath, fileName),
		Size:      fileSize,
		ChannelID: channelID,
		Encrypted: d.EncryptFiles,
		Parts:     fileChunks,
		ModTime:   file.ModTime(),
	}

	resp, err = d.client.R().
		SetBody(createFileReq).
		Post("/api/files")

	if err != nil {
		return nil, fmt.Errorf("创建文件失败: %v", err)
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("创建文件失败: %s", resp.String())
	}

	// 解析文件信息
	var fileInfo FileInfo
	err = json.Unmarshal(resp.Body(), &fileInfo)
	if err != nil {
		return nil, fmt.Errorf("解析文件信息失败: %v", err)
	}

	return &Object{
		ID:       fileInfo.Id,
		Name:     fileName,
		Size:     fileSize,
		ModTime:  fileInfo.ModTime,
		IsFolder: false,
		Path:     path.Join(parentPath, fileName),
		ParentID: fileInfo.ParentId,
		driver:   d,
	}, nil
}

func (d *TelDrive) Remove(ctx context.Context, obj model.Obj) error {
	telObj, ok := obj.(*Object)
	if !ok {
		return fmt.Errorf("无效的对象类型")
	}

	resp, err := d.client.R().
		SetBody(map[string]interface{}{
			"ids": []string{telObj.ID},
		}).
		Delete("/api/files")

	if err != nil {
		return err
	}

	if resp.StatusCode() != 200 {
		return fmt.Errorf("删除文件失败: %s", resp.String())
	}

	return nil
}

func (d *TelDrive) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	telObj, ok := srcObj.(*Object)
	if !ok {
		return nil, fmt.Errorf("无效的对象类型")
	}

	resp, err := d.client.R().
		SetBody(map[string]interface{}{
			"name": newName,
		}).
		Patch("/api/files/" + telObj.ID)

	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("重命名文件失败: %s", resp.String())
	}

	// 更新对象信息
	newObj := *telObj
	newObj.Name = newName
	newObj.Path = path.Join(path.Dir(telObj.Path), newName)

	return &newObj, nil
}

func (d *TelDrive) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	telObj, ok := srcObj.(*Object)
	if !ok {
		return nil, fmt.Errorf("无效的源对象类型")
	}

	dstDirObj, ok := dstDir.(*Object)
	if !ok {
		return nil, fmt.Errorf("无效的目标目录类型")
	}

	resp, err := d.client.R().
		SetBody(map[string]interface{}{
			"destinationParent": dstDirObj.ID,
			"ids":              []string{telObj.ID},
		}).
		Post("/api/files/move")

	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("移动文件失败: %s", resp.String())
	}

	// 更新对象信息
	newObj := *telObj
	newObj.ParentID = dstDirObj.ID
	newObj.Path = path.Join(dstDirObj.Path, telObj.Name)

	return &newObj, nil
}

var _ driver.Driver = (*TelDrive)(nil)