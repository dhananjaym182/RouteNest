package main

import "net/http"

// GET /admin/api/combos
func handleCombosList(w http.ResponseWriter, r *http.Request) {
	adminCombos.List(w, r)
}

// POST /admin/api/combos/save  body: ModelCombo
func handleComboSave(w http.ResponseWriter, r *http.Request) {
	adminCombos.Save(w, r)
}

// POST /admin/api/combos/delete  body: {id}
func handleComboDelete(w http.ResponseWriter, r *http.Request) {
	adminCombos.Delete(w, r)
}

// POST /admin/api/combos/toggle  body: {id, enabled}
func handleComboToggle(w http.ResponseWriter, r *http.Request) {
	adminCombos.Toggle(w, r)
}

// POST /admin/api/combos/clone  body: {id}
func handleComboClone(w http.ResponseWriter, r *http.Request) {
	adminCombos.Clone(w, r)
}
