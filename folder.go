package model

import (
	"errors"
	"path"
	"time"

	"github.com/cloudreve/Cloudreve/v3/pkg/util"
	"github.com/jinzhu/gorm"
)

// Folder 
type Folder struct {
	// 
	gorm.Model
	Name     string `gorm:"unique_index:idx_only_one_name"`
	ParentID *uint  `gorm:"index:parent_id;unique_index:idx_only_one_name"`
	OwnerID  uint   `gorm:"index:owner_id"`

	// 
	Position      string `gorm:"-"`
	WebdavDstName string `gorm:"-"`
}

// Create 
func (folder *Folder) Create() (uint, error) {
	if err := DB.FirstOrCreate(folder, *folder).Error; err != nil {
		folder.Model = gorm.Model{}
		err2 := DB.First(folder, *folder).Error
		return folder.ID, err2
	}

	return folder.ID, nil
}

// 
func (folder *Folder) GetChild(name string) (*Folder, error) {
	var resFolder Folder
	err := DB.
		Where("parent_id = ? AND owner_id = ? AND name = ?", folder.ID, folder.OwnerID, name).
		First(&resFolder).Error

	
	if err == nil {
		resFolder.Position = path.Join(folder.Position, folder.Name)
	}
	return &resFolder, err
}

// TraceRoot 
func (folder *Folder) TraceRoot() error {
	if folder.ParentID == nil {
		return nil
	}

	var parentFolder Folder
	err := DB.
		Where("id = ? AND owner_id = ?", folder.ParentID, folder.OwnerID).
		First(&parentFolder).Error

	if err == nil {
		err := parentFolder.TraceRoot()
		folder.Position = path.Join(parentFolder.Position, parentFolder.Name)
		return err
	}

	return err
}

// GetChildFolder 
func (folder *Folder) GetChildFolder() ([]Folder, error) {
	var folders []Folder
	result := DB.Where("parent_id = ?", folder.ID).Find(&folders)

	if result.Error == nil {
		for i := 0; i < len(folders); i++ {
			folders[i].Position = path.Join(folder.Position, folder.Name)
		}
	}
	return folders, result.Error
}

// GetRecursiveChildFolder 
func GetRecursiveChildFolder(dirs []uint, uid uint, includeSelf bool) ([]Folder, error) {
	folders := make([]Folder, 0, len(dirs))
	var err error

	var parFolders []Folder
	result := DB.Where("owner_id = ? and id in (?)", uid, dirs).Find(&parFolders)
	if result.Error != nil {
		return folders, err
	}

	// 
	var parentIDs = make([]uint, 0, len(parFolders))
	for _, folder := range parFolders {
		parentIDs = append(parentIDs, folder.ID)
	}

	if includeSelf {
		// 
		folders = append(folders, parFolders...)
	}
	parFolders = []Folder{}

	// 
	for i := 0; i < 65535; i++ {

		result = DB.Where("owner_id = ? and parent_id in (?)", uid, parentIDs).Find(&parFolders)

		// 
		if len(parFolders) == 0 {
			break
		}

		// 
		parentIDs = make([]uint, 0, len(parFolders))
		for _, folder := range parFolders {
			parentIDs = append(parentIDs, folder.ID)
		}

		// 
		folders = append(folders, parFolders...)
		parFolders = []Folder{}

	}

	return folders, err
}

// DeleteFolderByIDs 
func DeleteFolderByIDs(ids []uint) error {
	result := DB.Where("id in (?)", ids).Unscoped().Delete(&Folder{})
	return result.Error
}

// GetFoldersByIDs 
func GetFoldersByIDs(ids []uint, uid uint) ([]Folder, error) {
	var folders []Folder
	result := DB.Where("id in (?) AND owner_id = ?", ids, uid).Find(&folders)
	return folders, result.Error
}

// MoveOrCopyFileTo 
func (folder *Folder) MoveOrCopyFileTo(files []uint, dstFolder *Folder, isCopy bool) (uint64, error) {
	var copiedSize uint64

	if isCopy {
		var originFiles = make([]File, 0, len(files))
		if err := DB.Where(
			"id in (?) and user_id = ? and folder_id = ?",
			files,
			folder.OwnerID,
			folder.ID,
		).Find(&originFiles).Error; err != nil {
			return 0, err
		}

		for _, oldFile := range originFiles {
			if !oldFile.CanCopy() {
				util.Log().Warning("Cannot copy file %q because it's being uploaded now, skipping...", oldFile.Name)
				continue
			}

			oldFile.Model = gorm.Model{}
			oldFile.FolderID = dstFolder.ID
			oldFile.UserID = dstFolder.OwnerID

			if dstFolder.WebdavDstName != "" {
				oldFile.Name = dstFolder.WebdavDstName
			}

			if err := DB.Create(&oldFile).Error; err != nil {
				return copiedSize, err
			}

			copiedSize += oldFile.Size
		}

	} else {
		var updates = map[string]interface{}{
			"folder_id": dstFolder.ID,
		}
		if dstFolder.WebdavDstName != "" {
			updates["name"] = dstFolder.WebdavDstName
		}

		err := DB.Model(File{}).Where(
			"id in (?) and user_id = ? and folder_id = ?",
			files,
			folder.OwnerID,
			folder.ID,
		).
			Update(updates).
			Error
		if err != nil {
			return 0, err
		}

	}

	return copiedSize, nil

}

func (folder *Folder) CopyFolderTo(folderID uint, dstFolder *Folder) (size uint64, err error) {
	subFolders, err := GetRecursiveChildFolder([]uint{folderID}, folder.OwnerID, true)
	if err != nil {
		return 0, err
	}

	
	var subFolderIDs = make([]uint, len(subFolders))
	for key, value := range subFolders {
		subFolderIDs[key] = value.ID
	}

	var newIDCache = make(map[uint]uint)
	for _, folder := range subFolders {
		var newID uint
		if folder.ID == folderID {
			newID = dstFolder.ID
			// webdav
			if dstFolder.WebdavDstName != "" {
				folder.Name = dstFolder.WebdavDstName
			}
		} else if IDCache, ok := newIDCache[*folder.ParentID]; ok {
			newID = IDCache
		} else {
			util.Log().Warning("Failed to get parent folder %q", *folder.ParentID)
			return size, errors.New("Failed to get parent folder")
		}

		oldID := folder.ID
		folder.Model = gorm.Model{}
		folder.ParentID = &newID
		folder.OwnerID = dstFolder.OwnerID
		if err = DB.Create(&folder).Error; err != nil {
			return size, err
		}
		
		newIDCache[oldID] = folder.ID

	}

	// 
	var originFiles = make([]File, 0, len(subFolderIDs))
	if err := DB.Where(
		"user_id = ? and folder_id in (?)",
		folder.OwnerID,
		subFolderIDs,
	).Find(&originFiles).Error; err != nil {
		return 0, err
	}

	// 
	for _, oldFile := range originFiles {
		if !oldFile.CanCopy() {
			util.Log().Warning("Cannot copy file %q because it's being uploaded now, skipping...", oldFile.Name)
			continue
		}

		oldFile.Model = gorm.Model{}
		oldFile.FolderID = newIDCache[oldFile.FolderID]
		oldFile.UserID = dstFolder.OwnerID
		if err := DB.Create(&oldFile).Error; err != nil {
			return size, err
		}

		size += oldFile.Size
	}

	return size, nil

}


func (folder *Folder) MoveFolderTo(dirs []uint, dstFolder *Folder) error {

	
	if folder.OwnerID == dstFolder.OwnerID && util.ContainsUint(dirs, dstFolder.ID) {
		return errors.New("cannot move a folder into itself")
	}

	var updates = map[string]interface{}{
		"parent_id": dstFolder.ID,
	}
	// webdav
	if dstFolder.WebdavDstName != "" {
		updates["name"] = dstFolder.WebdavDstName
	}

	// 更改顶级要移动目录的父目录指向
	err := DB.Model(Folder{}).Where(
		"id in (?) and owner_id = ? and parent_id = ?",
		dirs,
		folder.OwnerID,
		folder.ID,
	).Update(updates).Error

	return err

}

// Rename 
func (folder *Folder) Rename(new string) error {
	return DB.Model(&folder).UpdateColumn("name", new).Error
}


func (folder *Folder) GetName() string {
	return folder.Name
}

func (folder *Folder) GetSize() uint64 {
	return 0
}
func (folder *Folder) ModTime() time.Time {
	return folder.UpdatedAt
}
func (folder *Folder) IsDir() bool {
	return true
}
func (folder *Folder) GetPosition() string {
	return folder.Position
}
