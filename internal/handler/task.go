package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-image-tr/internal/service"
)

// taskHandler 提供转换任务的创建、查询、取消与清理接口。
type taskHandler struct {
	tasks *service.TaskService
	log   *zap.Logger
}

// list 返回任务列表（按创建时间倒序）。
func (h *taskHandler) list(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tasks": h.tasks.List()})
}

// stats 返回任务计数（供顶部状态条轮询）。
func (h *taskHandler) stats(c *gin.Context) {
	c.JSON(http.StatusOK, h.tasks.Stats())
}

// get 返回单个任务。
func (h *taskHandler) get(c *gin.Context) {
	task, ok := h.tasks.Get(c.Param("id"))
	if !ok {
		respondMsg(c, http.StatusNotFound, "任务不存在")
		return
	}
	c.JSON(http.StatusOK, task)
}

// create 批量创建转换任务。
func (h *taskHandler) create(c *gin.Context) {
	var spec service.TaskSpec
	if err := c.ShouldBindJSON(&spec); err != nil {
		respondMsg(c, http.StatusBadRequest, "请求格式错误: "+errMessage(err))
		return
	}
	tasks, err := h.tasks.Create(c.Request.Context(), spec)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tasks": tasks,
		"count": len(tasks),
		"batch": firstBatchID(tasks),
	})
}

// cancel 取消排队中或执行中的任务。
func (h *taskHandler) cancel(c *gin.Context) {
	if err := h.tasks.Cancel(c.Param("id")); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// retry 重新入队失败或已取消的任务。
func (h *taskHandler) retry(c *gin.Context) {
	task, err := h.tasks.Retry(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, task)
}

// remove 删除已结束的任务记录（产物文件保留）。
func (h *taskHandler) remove(c *gin.Context) {
	if err := h.tasks.Delete(c.Param("id")); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// clear 清理已结束的任务。
func (h *taskHandler) clear(c *gin.Context) {
	var req struct {
		// KeepOutputs 为 true 时保留产物文件，仅清理任务记录
		KeepOutputs bool `json:"keep_outputs"`
	}
	_ = c.ShouldBindJSON(&req) // 允许空 body，默认保留产物
	n, err := h.tasks.Clear(req.KeepOutputs)
	if err != nil {
		respondMsg(c, http.StatusInternalServerError, errMessage(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"removed": n})
}

func firstBatchID(tasks []*service.Task) string {
	if len(tasks) == 0 {
		return ""
	}
	return tasks[0].BatchID
}
