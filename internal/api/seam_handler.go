package api

import "net/http"

func (handler *Handler) seams(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, handler.runtime.SeamReport())
}
