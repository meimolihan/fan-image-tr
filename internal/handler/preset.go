package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/service"
)

// presetHandler 提供转换预设的增删查接口。
type presetHandler struct {
	presets *service.PresetService
	log     *zap.Logger
}

// list 返回全部预设（内置 + 用户自定义）。
func (h *presetHandler) list(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"presets": h.presets.List()})
}

// save 保存（新增或覆盖）一个预设。
func (h *presetHandler) save(c *gin.Context) {
	var p service.Preset
	if err := c.ShouldBindJSON(&p); err != nil {
		respondMsg(c, http.StatusBadRequest, "请求格式错误: "+errMessage(err))
		return
	}
	if err := h.presets.Save(&p); err != nil {
		respondMsg(c, http.StatusBadRequest, errMessage(err))
		return
	}
	saved, _ := h.presets.Get(p.Name)
	c.JSON(http.StatusOK, saved)
}

// remove 删除一个预设。
func (h *presetHandler) remove(c *gin.Context) {
	if err := h.presets.Delete(c.Param("name")); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
