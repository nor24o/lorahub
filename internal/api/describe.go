package api

import "net/http"

// requestDescription asks a node to describe the commands it accepts
// (model.DescribeRequest). A class A node gets the request after its next uplink.
func (s *Server) requestDescription(w http.ResponseWriter, r *http.Request) {
	q, err := s.Proc.RequestDescription(r.PathValue("app"), r.PathValue("dev"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 202, q)
}
