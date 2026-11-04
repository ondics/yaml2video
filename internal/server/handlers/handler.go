package handlers

import "github.com/ondics/yaml2video/internal/server/controller"

// Handler translates HTTP requests and responses to controller operations.
type Handler struct {
	Controller *controller.Controller
}
