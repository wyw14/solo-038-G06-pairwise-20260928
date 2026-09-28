package api

import (
	"net/http"

	"github.com/wyw14/solo-038/internal/service"
)

func (handler *Handler) inspectSplice(writer http.ResponseWriter, request *http.Request) {
	var input service.SeamInspectionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, err)
		return
	}
	view, err := handler.runtime.InspectSplice(request.Context(), input)
	if err != nil {
		writeError(writer, http.StatusConflict, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, view)
}

func (handler *Handler) spliceInspectionStatus(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, handler.runtime.SpliceInspectionStatus())
}
