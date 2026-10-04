// LoL Shard WebUI — lokale Go-Bridge
// -----------------------------------
// Liest das Lockfile des laufenden LoL-Clients, spricht dessen lokale
// LCU-HTTPS-API an und stellt die Web-UI + API auf http://127.0.0.1:PORT bereit.
// Nur Go-Standardbibliothek. Absicherung gegen fremde Webseiten:
//  - die offizielle WebUI-Origin (defaultAllowedOrigin) ist standardmäßig erlaubt
//  - weitere Ursprünge per --allow-origin; entfernte Frontends per --key (X-UI-Key)
//
// Bauen:    go build -trimpath -ldflags "-s -w" -o bridge.exe .
// Starten:  bridge.exe  (Doppelklick) — für weitere Ursprünge: bridge.exe --allow-origin https://...
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)


// ---------------------------------------------------------------------------
// Lockfile-Erkennung
// ---------------------------------------------------------------------------

var lockfileStatic = []string{
	`C:\Riot Games\League of Legends\lockfile`,
	`D:\Riot Games\League of Legends\lockfile`,
	`E:\Riot Games\League of Legends\lockfile`,
}

var lockRE = regexp.MustCompile(`^[^:]+:(\d+):(\d+):([^:]+):(https|wss)$`)

type Lockfile struct {
	Port  int
	Token string
}

func lockfileCandidates() []string {
	set := map[string]bool{}
	for _, c := range lockfileStatic {
		set[c] = true
	}
	for _, root := range []string{`C:\Riot Games`, `D:\Riot Games`, `E:\Riot Games`} {
		matches, _ := filepath.Glob(filepath.Join(root, "*", "lockfile"))
		for _, m := range matches {
			d := strings.TrimSpace(strings.ToLower(filepath.ToSlash(filepath.Dir(m))))
			if strings.HasSuffix(d, "league-of-legends") || strings.HasSuffix(d, "league of legends") {
				set[m] = true
			}
		}
	}
	meta, _ := filepath.Glob(`C:\ProgramData\Riot Games\Metadata\league_of_legends*\**\*.lockfile`)
	for _, m := range meta {
		set[m] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func readLockfile(path string) *Lockfile {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m := lockRE.FindStringSubmatch(strings.TrimSpace(string(b)))
	if m == nil {
		return nil
	}
	port, _ := strconv.Atoi(m[2])
	return &Lockfile{Port: port, Token: m[3]}
}

func findLockfile() *Lockfile {
	for _, p := range lockfileCandidates() {
		if lf := readLockfile(p); lf != nil {
			return lf
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// LCU-HTTP-Client (selbstsigniertes Zertifikat ok)
// ---------------------------------------------------------------------------

type LcuError struct {
	Status  int
	Message string
}

func (e *LcuError) Error() string { return e.Message }

type Client struct {
	port  int
	tok   string
	httpc *http.Client
}

func NewClient(port int, token string) *Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &Client{port: port, tok: token, httpc: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
}

func (c *Client) Port() int   { return c.port }
func (c *Client) Token() string { return c.tok }

func (c *Client) do(method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	u := fmt.Sprintf("https://127.0.0.1:%d/%s", c.port, strings.TrimLeft(path, "/"))
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("riot:"+c.tok)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, &LcuError{0, err.Error()}
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		return nil, rerr
	}
	if resp.StatusCode >= 400 {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		var dm map[string]any
		if json.Unmarshal(raw, &dm) == nil {
			if s, ok := dm["message"].(string); ok && s != "" {
				msg = s
			}
		}
		return raw, &LcuError{resp.StatusCode, msg}
	}
	return raw, nil
}

func (c *Client) getJSON(path string, out any) error {
	raw, err := c.do("GET", path, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// ---------------------------------------------------------------------------
// Loot-Datenmodell
// ---------------------------------------------------------------------------

type LootItem struct {
	LootID           string `json:"lootId"`
	Count            int    `json:"count"`
	Type             string `json:"type"`
	ItemDesc         string `json:"itemDesc"`
	LocalizedName    string `json:"localizedName"`
	DisenchantValue  int    `json:"disenchantValue"`
	DisenchantRecipe string `json:"disenchantRecipeName"`
	TilePath         string `json:"tilePath"`
	RedeemableStatus string `json:"redeemableStatus"`
	Value            int    `json:"value"`
}

type lootSlot struct {
	LootIDs  []string `json:"lootIds"`
	Quantity int      `json:"quantity"`
}

type lcuRecipe struct {
	RecipeName string     `json:"recipeName"`
	Type       string     `json:"type"`
	Slots      []lootSlot `json:"slots"`
}

type RecipeInfo struct {
	Disenchant      string
	Upgrade         string
	UpgradeCost     int
	UpgradeCurrency string
}

type championOwnership struct {
	Owned bool `json:"owned"`
}

type championMinimal struct {
	ID        int               `json:"id"`
	Ownership championOwnership `json:"ownership"`
}

// ---------------------------------------------------------------------------
// Service: Rezepte + besessene Champions (mit Caches)
// ---------------------------------------------------------------------------

type Service struct {
	mu      sync.Mutex
	client  *Client
	recipes map[string]*RecipeInfo
	owned   map[int]bool
	ownedTS time.Time
}

func NewService(c *Client) *Service {
	return &Service{client: c, recipes: map[string]*RecipeInfo{}}
}

func (s *Service) setClient(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
	s.recipes = map[string]*RecipeInfo{}
	s.owned = nil
	s.ownedTS = time.Time{}
}

func (s *Service) recipesInfo(lootID string) *RecipeInfo {
	s.mu.Lock()
	if r, ok := s.recipes[lootID]; ok {
		s.mu.Unlock()
		return r
	}
	client := s.client
	s.mu.Unlock()

	var list []lcuRecipe
	res := &RecipeInfo{}
	if err := client.getJSON("/lol-loot/v1/recipes/initial-item/"+url.PathEscape(lootID), &list); err == nil {
		for _, r := range list {
			switch r.Type {
			case "DISENCHANT":
				res.Disenchant = r.RecipeName
			case "UPGRADE":
				res.Upgrade = r.RecipeName
				for _, slot := range r.Slots {
					for _, id := range slot.LootIDs {
						if strings.HasPrefix(id, "CURRENCY_") {
							res.UpgradeCost = slot.Quantity
							res.UpgradeCurrency = id
							break
						}
					}
				}
			}
		}
	}
	s.mu.Lock()
	s.recipes[lootID] = res
	s.mu.Unlock()
	return res
}

func (s *Service) ownedChampionIDs() map[int]bool {
	s.mu.Lock()
	if s.owned != nil && time.Since(s.ownedTS) < 60*time.Second {
		defer s.mu.Unlock()
		return s.owned
	}
	client := s.client
	s.mu.Unlock()

	var summoner struct {
		SummonerID int64 `json:"summonerId"`
	}
	ids := map[int]bool{}
	if err := client.getJSON("/lol-summoner/v1/current-summoner", &summoner); err == nil && summoner.SummonerID > 0 {
		var champs []championMinimal
		if err := client.getJSON(fmt.Sprintf("/lol-champions/v1/inventories/%d/champions-minimal", summoner.SummonerID), &champs); err == nil {
			for _, ch := range champs {
				if ch.Ownership.Owned {
					ids[ch.ID] = true
				}
			}
		}
	}
	s.mu.Lock()
	s.owned = ids
	s.ownedTS = time.Now()
	s.mu.Unlock()
	return ids
}

func championIDFromLoot(lootID string) (int, bool) {
	i := strings.LastIndex(lootID, "_")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(lootID[i+1:])
	return n, err == nil
}

func pick(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

func (s *Service) serialize(it LootItem, kind string, owned map[int]bool) map[string]any {
	rec := s.recipesInfo(it.LootID)
	count := it.Count
	if count < 1 {
		count = 1
	}
	name := it.ItemDesc
	if name == "" {
		name = it.LocalizedName
	}
	if name == "" {
		name = it.LootID
	}
	var ownedFlag any
	if kind == "champion" {
		if id, ok := championIDFromLoot(it.LootID); ok {
			ownedFlag = owned[id]
		}
	}
	return map[string]any{
		"lootId":           it.LootID,
		"kind":             kind,
		"name":             name,
		"owned":            ownedFlag,
		"count":            count,
		"disenchantValue":  it.DisenchantValue,
		"disenchantTotal":  it.DisenchantValue * count,
		"recipeDisenchant": pick(it.DisenchantRecipe, rec.Disenchant),
		"canUpgrade":       rec.Upgrade != "",
		"recipeUpgrade":    rec.Upgrade,
		"upgradeCurrency":  rec.UpgradeCurrency,
		"upgradeCost":      rec.UpgradeCost,
		"upgradeTotal":     rec.UpgradeCost * count,
		"tile":             it.TilePath,
		"status":           it.RedeemableStatus,
		"value":            it.Value,
	}
}

func (s *Service) summary() (map[string]any, error) {
	client := s.client
	var loot []LootItem
	if err := client.getJSON("/lol-loot/v1/player-loot", &loot); err != nil {
		return nil, err
	}
	cur := map[string]int64{}
	for _, it := range loot {
		if it.Type == "CURRENCY" {
			cur[it.LootID] = int64(it.Count)
		}
	}
	owned := s.ownedChampionIDs()
	champs := []map[string]any{}
	skins := []map[string]any{}
	wards := []map[string]any{}
	for _, it := range loot {
		switch it.Type {
		case "CHAMPION_RENTAL":
			champs = append(champs, s.serialize(it, "champion", owned))
		case "SKIN_RENTAL":
			skins = append(skins, s.serialize(it, "skin", owned))
		case "WARDSKIN_RENTAL":
			wards = append(wards, s.serialize(it, "ward", owned))
		}
	}
	sm := map[string]any{"gameName": "Spieler", "tagLine": "", "summonerLevel": 0}
	var summoner struct {
		GameName      string `json:"gameName"`
		TagLine       string `json:"tagLine"`
		SummonerLevel int    `json:"summonerLevel"`
	}
	if client.getJSON("/lol-summoner/v1/current-summoner", &summoner) == nil && summoner.GameName != "" {
		sm = map[string]any{"gameName": summoner.GameName, "tagLine": summoner.TagLine, "summonerLevel": summoner.SummonerLevel}
	}
	return map[string]any{
		"championShards": champs,
		"skinShards":     skins,
		"wardShards":     wards,
		"essence":        map[string]int64{"blue": cur["CURRENCY_champion"], "orange": cur["CURRENCY_cosmetic"]},
		"summoner":       sm,
	}, nil
}

// ---------------------------------------------------------------------------
// Craft-Ausführung
// ---------------------------------------------------------------------------

type Action struct {
	LootID string `json:"lootId"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Count  int    `json:"count"`
}

type CraftResult struct {
	LootID  string `json:"lootId"`
	Name    string `json:"name"`
	Action  string `json:"action"`
	Count   int    `json:"count"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func findLoot(loot []LootItem, id string) *LootItem {
	for i := range loot {
		if loot[i].LootID == id {
			return &loot[i]
		}
	}
	return nil
}

func currencyPool(c *Client, currencyID string) (int64, bool) {
	var loot []LootItem
	if c.getJSON("/lol-loot/v1/player-loot", &loot) != nil {
		return 0, false
	}
	for _, it := range loot {
		if it.LootID == currencyID {
			return int64(it.Count), true
		}
	}
	return 0, false
}

func (a *App) performCrafts(actions []Action) []CraftResult {
	var results []CraftResult
	var loot []LootItem
	a.lockList(&loot)
	for _, ac := range actions {
		r := CraftResult{LootID: ac.LootID, Name: ac.Name, Action: ac.Action, Count: ac.Count}
		item := findLoot(loot, ac.LootID)
		count := ac.Count
		if count < 1 {
			count = 1
		}
		if item == nil {
			r.Message = "Loot nicht gefunden (möglicherweise bereits verbraucht)"
			results = append(results, r)
			continue
		}
		var recipe string
		var currency string
		if ac.Action == "disenchant" {
			recipe = pick(item.DisenchantRecipe, a.service.recipesInfo(ac.LootID).Disenchant)
			if recipe == "" {
				r.Message = "Kein Entzauber-Rezept vorhanden"
				results = append(results, r)
				continue
			}
		} else {
			rec := a.service.recipesInfo(ac.LootID)
			recipe = rec.Upgrade
			if recipe == "" {
				r.Message = "Nicht aktivierbar (Champion/Skin bereits permanent?)"
				results = append(results, r)
				continue
			}
			currency = rec.UpgradeCurrency
			if currency != "" {
				need := rec.UpgradeCost * count
				pool, ok := currencyPool(a.getClient(), currency)
				if ok && pool < int64(need) {
					r.Message = fmt.Sprintf("Zu wenig Essenz: braucht %d von %s/%d", need, currency, pool)
					results = append(results, r)
					continue
				}
			}
		}
		ok := true
		for i := 0; i < count; i++ {
			if err := a.craftOnce(recipe, ac.LootID, currency); err != nil {
				ok = false
				r.Message = err.Error()
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		tip := "entzaubert"
		if ac.Action == "upgrade" {
			tip = "aktiviert"
		}
		if ok {
			r.Message = fmt.Sprintf("%d x %s erfolgreich %s", count, ac.Name, tip)
		}
		r.OK = ok
		results = append(results, r)
	}
	return results
}

// ---------------------------------------------------------------------------
// App / Web-Server
// ---------------------------------------------------------------------------

type imgEntry struct {
	data  []byte
	ctype string
}

type App struct {
	mu      sync.Mutex
	imgMu   sync.Mutex
	client  *Client
	service *Service
	hub     *Hub
	imgCache map[string]imgEntry
	key     string
	origins []string
}

func (a *App) getClient() *Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil
	}
	return a.client
}

func (a *App) getService() *Service {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.service
}

func (a *App) lockList(out *[]LootItem) bool {
	c := a.getClient()
	if c == nil {
		return false
	}
	return c.getJSON("/lol-loot/v1/player-loot", out) == nil
}

func (a *App) craftOnce(recipe, lootID, currency string) error {
	ids := []string{lootID}
	if currency != "" {
		ids = append(ids, currency)
	}
	c := a.getClient()
	if c == nil {
		return &LcuError{0, "keine LCU-Verbindung"}
	}
	_, err := c.do("POST", "/lol-loot/v1/recipes/"+url.PathEscape(recipe)+"/craft", ids)
	if err != nil {
		if le, ok := err.(*LcuError); ok && le.Status == 0 && a.refresh() {
			c2 := a.getClient()
			if _, err2 := c2.do("POST", "/lol-loot/v1/recipes/"+url.PathEscape(recipe)+"/craft", ids); err2 == nil {
				return nil
			}
		}
	}
	return err
}

func (a *App) refresh() bool {
	lf := findLockfile()
	if lf == nil {
		return false
	}
	c := NewClient(lf.Port, lf.Token)
	if err := c.getJSON("/lol-summoner/v1/current-summoner", &map[string]any{}); err != nil {
		return false
	}
	a.mu.Lock()
	a.client = c
	a.service.setClient(c)
	a.mu.Unlock()
	log.Printf("Reconnect: neuer LCU-Endpunkt (Port %d).", lf.Port)
	return true
}

func (a *App) watchdog() {
	for {
		time.Sleep(3 * time.Second)
		lf := findLockfile()
		c := a.getClient()
		if c != nil && lf != nil && lf.Port == c.Port() && lf.Token == c.Token() {
			continue
		}
		if lf != nil {
			a.refresh()
		}
	}
}

func (a *App) poller() {
	var prev string
	for {
		time.Sleep(5 * time.Second)
		var loot []LootItem
		if !a.lockList(&loot) {
			continue
		}
		parts := make([]string, 0, len(loot))
		for _, it := range loot {
			parts = append(parts, it.LootID+":"+strconv.Itoa(it.Count))
		}
		sort.Strings(parts)
		sig := strings.Join(parts, "|")
		if prev != "" && sig != prev {
			a.hub.broadcast("loot")
		}
		prev = sig
	}
}

// --- CORS / Sicherheit ---

func (a *App) corsAndGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Für Chrome/Firefox "Private Network Access": erlaubt lokale Bridges
		// von öffentlichen Webseiten aus zu erreichen (Sicherheits-Preflight).
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		origin := r.Header.Get("Origin")
		if origin != "" && !sameOrigin(r, origin) {
			allow := false
			for _, o := range a.origins {
				if o == origin {
					allow = true
					break
				}
			}
			if allow {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				if r.Method == http.MethodOptions {
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-UI-Key")
					w.Header().Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
			} else if a.key != "" && r.Header.Get("X-UI-Key") == a.key {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				http.Error(w, "Zugriff von diesem Ursprung abgelehnt.", http.StatusForbidden)
				return
			}
		}
		next(w, r)
	}
}

func sameOrigin(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host != r.Host {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost":
		return true
	}
	return false
}

// --- SSE-Push ---

type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newHub() *Hub {
	return &Hub{subs: map[chan []byte]struct{}{}}
}

func (h *Hub) add(c chan []byte)  { h.mu.Lock(); h.subs[c] = struct{}{}; h.mu.Unlock() }
func (h *Hub) remove(c chan []byte) { h.mu.Lock(); delete(h.subs, c); h.mu.Unlock() }

func (h *Hub) broadcast(name string) {
	payload := []byte(fmt.Sprintf("id: %d\nevent: %s\ndata: 1\n\n", time.Now().UnixNano()/1e6, name))
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- payload:
		default:
		}
	}
}

func (a *App) streamHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ch := make(chan []byte, 8)
	a.hub.add(ch)
	defer a.hub.remove(ch)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	w.Write([]byte(": hello\n\n"))
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			w.Write(m)
			fl.Flush()
		case <-tick.C:
			w.Write([]byte(": keepalive\n\n"))
			fl.Flush()
		}
	}
}

// --- JSON-Handler ---

func writeJSON(w http.ResponseWriter, obj any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(obj)
}

func (a *App) lootHandler(w http.ResponseWriter, r *http.Request) {
	s := a.getService()
	if s == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "keine Verbindung zum League-Client"})
		return
	}
	summary, err := s.summary()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	summary["ok"] = true
	writeJSON(w, summary)
}

func (a *App) craftHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Actions []Action `json:"actions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "ungültiger Request"})
		return
	}
	results := a.performCrafts(body.Actions)
	writeJSON(w, map[string]any{"ok": true, "results": results})
}

func (a *App) imgHandler(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	if !strings.HasPrefix(p, "/lol-game-data/") {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid path"})
		return
	}
	a.imgMu.Lock()
	e, ok := a.imgCache[p]
	a.imgMu.Unlock()
	if !ok {
		c := a.getClient()
		if c == nil {
			writeJSON(w, map[string]any{"ok": false, "error": "image unavailable"})
			return
		}
		data, err := c.do("GET", p, nil)
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "image unavailable"})
			return
		}
		ctype := "image/jpeg"
		if strings.HasSuffix(strings.ToLower(p), ".png") {
			ctype = "image/png"
		}
		e = imgEntry{data: data, ctype: ctype}
		a.imgMu.Lock()
		if len(a.imgCache) > 400 {
			a.imgCache = map[string]imgEntry{}
		}
		a.imgCache[p] = e
		a.imgMu.Unlock()
	}
	w.Header().Set("Content-Type", e.ctype)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(e.data)
}

// ---------------------------------------------------------------------------
// Alternativer Web-Client: Profil, Rang, Historie, Freunde, Gameflow, Collections
// ---------------------------------------------------------------------------

type collectionOwnership struct {
	Owned bool `json:"owned"`
}

func (a *App) summonerInfo(c *Client) map[string]any {
	var sm struct {
		SummonerID int64  `json:"summonerId"`
		AccountID  int64  `json:"accountId"`
		PUUID      string `json:"puuid"`
		GameName   string `json:"gameName"`
		TagLine    string `json:"tagLine"`
		Level      int    `json:"summonerLevel"`
		Icon       int    `json:"profileIconId"`
	}
	out := map[string]any{}
	if c.getJSON("/lol-summoner/v1/current-summoner", &sm) == nil {
		out = map[string]any{
			"id": sm.SummonerID, "accountId": sm.AccountID, "puuid": sm.PUUID,
			"name": sm.GameName, "tag": sm.TagLine,
			"level": sm.Level, "icon": sm.Icon,
			"iconPath": fmt.Sprintf("/lol-game-data/assets/v1/profile-icons/%d.jpg", sm.Icon),
		}
	}
	return out
}

// HistoryPfad-Kandidaten: Je nach Client-Version akzeptiert der
// Match-History-Plugin accountId (RID), dessen lower-32-Bits (legacy)
// oder puuid als Routensegment. Wir probieren in dieser Reihenfolge.
func matchHistoryCandidates(c *Client) []string {
	var sm struct {
		SummonerID int64  `json:"summonerId"`
		AccountID  int64  `json:"accountId"`
		PUUID      string `json:"puuid"`
	}
	if c.getJSON("/lol-summoner/v1/current-summoner", &sm) != nil {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	add(strconv.FormatInt(sm.AccountID, 10))
	add(strconv.FormatInt(sm.SummonerID, 10))
	if sm.SummonerID != 0 {
		add(strconv.FormatInt(sm.SummonerID&0xFFFFFFFF, 10))
	}
	if sm.PUUID != "" {
		ids = append(ids, "puuid/"+sm.PUUID)
		add(sm.PUUID)
	}
	var paths []string
	for _, id := range ids {
		paths = append(paths, "/lol-match-history/v1/products/lol/"+id)
	}
	return paths
}

// firstWorkingHistoryPath liefert die Basis-URL, unter der der Match-History-Endpunkt
// echte Spieledaten liefert (HTTP 200, kein Plugin-Fehler).
func firstWorkingHistoryPath(c *Client) (string, bool) {
	for _, base := range matchHistoryCandidates(c) {
		raw, err := c.do("GET", base+"/matches?begIndex=0&endIndex=5", nil)
		if err == nil && !bytes.Contains(raw, []byte("could not find summoner info")) {
			return base, true
		}
	}
	return "", false
}

type lcuQueue struct {
	ID                int      `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	GameMode          string   `json:"gameMode"`
	QueueAvailability []string `json:"queueAvailability"`
	Type              string   `json:"type"`
}

// playableQueues liefert die im Launcher auswählbaren Warteschlangen (keine
// Custom- und keine TFT-Games) samt id→Name-Mapping für die Match-History.
func (a *App) playableQueues(c *Client) ([]map[string]any, map[int]string) {
	var all []lcuQueue
	names := map[int]string{}
	if c.getJSON("/lol-game-queues/v1/queues", &all) != nil {
		return nil, names
	}
	enabled := func(q lcuQueue) bool {
		if len(q.QueueAvailability) == 0 {
			return true
		}
		for _, s := range q.QueueAvailability {
			if s == "Enabled" || s == "Available" {
				return true
			}
		}
		return false
	}
	seen := map[int]bool{}
	out := []map[string]any{}
	for _, q := range all {
		names[q.ID] = q.Name
		if q.ID == 0 || q.Name == "" || !enabled(q) {
			continue
		}
		if q.Type == "CUSTOM_GAME" || q.GameMode == "TFT" || q.GameMode == "PRACTICETOOL" {
			continue
		}
		if seen[q.ID] {
			continue
		}
		seen[q.ID] = true
		out = append(out, map[string]any{"id": q.ID, "name": q.Name, "gameMode": q.GameMode})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"].(string) < out[j]["name"].(string) })
	return out, names
}

func (a *App) clientHandler(w http.ResponseWriter, r *http.Request) {
	c := a.getClient()
	if c == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "keine Verbindung zum League-Client"})
		return
	}
	out := map[string]any{"ok": true}
	sm := a.summonerInfo(c)
	out["summoner"] = sm
	sid, _ := sm["id"].(int64)

	queues, qnames := a.playableQueues(c)
	if len(queues) > 0 {
		out["queues"] = queues
	}

	var phase string
	if c.getJSON("/lol-gameflow/v1/gameflow-phase", &phase) == nil {
		out["gameflow"] = phase
	}

	// Champion-Map + Besitz (für Historie + Collections)
	champNames := map[int]string{}
	var inv []struct {
		ID   int                  `json:"id"`
		Name string               `json:"name"`
		Own  collectionOwnership  `json:"ownership"`
	}
	ownedChamps := 0
	totalChamps := 0
	if sid != 0 && c.getJSON(fmt.Sprintf("/lol-champions/v1/inventories/%d/champions", sid), &inv) == nil {
		totalChamps = len(inv)
		for _, ch := range inv {
			champNames[ch.ID] = ch.Name
			if ch.Own.Owned {
				ownedChamps++
			}
		}
	}
	out["collections"] = map[string]any{"owned": ownedChamps, "total": totalChamps}

	// Match-History (letzte 5)
	history := []map[string]any{}
	if hp, okHP := firstWorkingHistoryPath(c); okHP {
		var hist struct {
			Games struct {
				Games []struct {
					GameID  int64  `json:"gameId"`
					Created int64  `json:"gameCreation"`
					Mode    string `json:"gameMode"`
					QueueID int    `json:"queueId"`
					Parts   []struct {
						SummonerID int64  `json:"summonerId"`
						Puuid      string `json:"puuid"`
						ChampionID int    `json:"championId"`
						Stats      struct {
							Win     bool  `json:"win"`
							Kills   int   `json:"kills"`
							Deaths  int   `json:"deaths"`
							Assists int   `json:"assists"`
							CS      int   `json:"totalMinionsKilled"`
						} `json:"stats"`
					} `json:"participants"`
				} `json:"games"`
			} `json:"games"`
		}
		if c.getJSON(hp+"/matches?begIndex=0&endIndex=5", &hist) == nil {
			puuid, _ := sm["puuid"].(string)
			for _, g := range hist.Games.Games {
				row := map[string]any{"mode": g.Mode, "created": g.Created, "qid": g.QueueID, "qname": qnames[g.QueueID]}
				first := -1
				for i, p := range g.Parts {
					if p.SummonerID == sid || (puuid != "" && p.Puuid == puuid) {
						first = i
						break
					}
					if first < 0 && i == 0 {
						first = 0
					}
				}
				if first >= 0 {
					p := g.Parts[first]
					ch := champNames[p.ChampionID]
					if ch == "" {
						ch = fmt.Sprintf("#%d", p.ChampionID)
					}
					row["champion"] = ch
					row["win"] = p.Stats.Win
					row["kills"] = p.Stats.Kills
					row["deaths"] = p.Stats.Deaths
					row["assists"] = p.Stats.Assists
					row["cs"] = p.Stats.CS
				}
				history = append(history, row)
			}
		}
	}
	out["history"] = history

	// Freunde (erste 30, readonly) – Name liegt in gameName/gameTag, nicht name.
	var friends []struct {
		GameName string `json:"gameName"`
		GameTag  string `json:"gameTag"`
		Avail    string `json:"availability"`
		Note     string `json:"statusMessage"`
	}
	if c.getJSON("/lol-chat/v1/friends", &friends) == nil {
		fs := []map[string]any{}
		for _, f := range friends {
			if len(fs) >= 30 {
				break
			}
			nm := f.GameName
			if f.GameName == "" {
				nm = "?"
			}
			if f.GameTag != "" {
				nm += " #" + f.GameTag
			}
			fs = append(fs, map[string]any{"name": nm, "avail": f.Avail, "note": f.Note})
		}
		out["friends"] = fs
	}

	// Rang (best effort)
	var rrs struct {
		QueueMap map[string]struct {
			Tier     string `json:"tier"`
			Division string `json:"division"`
			LP       int    `json:"leaguePoints"`
		} `json:"queueMap"`
	}
	if c.getJSON("/lol-ranked/v1/current-ranked-stats", &rrs) == nil {
		ranks := []map[string]any{}
		for q, v := range rrs.QueueMap {
			ranks = append(ranks, map[string]any{"queue": q, "tier": v.Tier, "division": v.Division, "lp": v.LP})
		}
		sort.Slice(ranks, func(i, j int) bool {
			return ranks[i]["queue"].(string) < ranks[j]["queue"].(string)
		})
		out["rank"] = ranks
	}

	writeJSON(w, out)
}

func (a *App) collectionsHandler(w http.ResponseWriter, r *http.Request) {
	c := a.getClient()
	if c == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "keine Verbindung zum League-Client"})
		return
	}
	out := map[string]any{"ok": true}
	sm := a.summonerInfo(c)
	out["summoner"] = sm
	sid, ok := sm["id"].(int64)
	if !ok || sid == 0 {
		writeJSON(w, out)
		return
	}

	var inv []struct {
		ID   int                 `json:"id"`
		Name string              `json:"name"`
		Own  collectionOwnership `json:"ownership"`
	}
	owned := 0
	if c.getJSON(fmt.Sprintf("/lol-champions/v1/inventories/%d/champions", sid), &inv) == nil {
		for _, ch := range inv {
			if ch.Own.Owned {
				owned++
			}
		}
		out["champions"] = map[string]any{"owned": owned, "total": len(inv)}
	}

	var skins []struct {
		ID      int                 `json:"id"`
		ChampID int                 `json:"championId"`
		Name    string              `json:"name"`
		Own     collectionOwnership `json:"ownership"`
	}
	ownedSkins := 0
	skinNames := []string{}
	if c.getJSON(fmt.Sprintf("/lol-collections/v1/inventories/%d/skins", sid), &skins) == nil {
		for _, sk := range skins {
			if sk.Own.Owned {
				ownedSkins++
				if len(skinNames) < 200 {
					skinNames = append(skinNames, sk.Name)
				}
			}
		}
		sort.Strings(skinNames)
		out["skins"] = map[string]any{"owned": ownedSkins, "names": skinNames}
	}

	writeJSON(w, out)
}

func (a *App) playHandler(w http.ResponseWriter, r *http.Request) {
	c := a.getClient()
	if c == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "keine Verbindung zum League-Client"})
		return
	}
	var body struct {
		Queue int `json:"queue"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	if body.Queue == 0 {
		body.Queue = 430
	}
	var phase string
	if c.getJSON("/lol-gameflow/v1/gameflow-phase", &phase) != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "Gameflow nicht verfügbar"})
		return
	}
	switch phase {
	case "Matchmaking":
		writeJSON(w, map[string]any{"ok": true, "searching": true, "phase": phase})
		return
	case "None":
		if _, err := c.do("POST", "/lol-lobby/v2/lobby", map[string]any{"queueId": body.Queue}); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": "Lobby: " + err.Error()})
			return
		}
	case "Lobby":
		// Bestehende Lobby prüfen: Queue identisch? Dann direkt nutzen,
		// sonst Lobby verlassen und mit gewünschter Queue neu anlegen.
		var cur struct {
			GameConfig struct {
				QueueID int `json:"queueId"`
			} `json:"gameConfig"`
		}
		sameQueue := c.getJSON("/lol-lobby/v2/lobby", &cur) == nil && cur.GameConfig.QueueID == body.Queue
		if !sameQueue {
			c.do("DELETE", "/lol-lobby/v2/lobby", nil)
			if _, err := c.do("POST", "/lol-lobby/v2/lobby", map[string]any{"queueId": body.Queue}); err != nil {
				writeJSON(w, map[string]any{"ok": false, "error": "Lobby: " + err.Error()})
				return
			}
		}
	default:
		writeJSON(w, map[string]any{"ok": false, "error": fmt.Sprintf("Nicht startbar in Phase %q", phase)})
		return
	}
	if _, err := c.do("POST", "/lol-lobby/v2/lobby/matchmaking/search", nil); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "Suche: " + err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "searching": true, "phase": phase})
}

func (a *App) cancelHandler(w http.ResponseWriter, r *http.Request) {
	c := a.getClient()
	if c == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "keine Verbindung zum League-Client"})
		return
	}
	var phase string
	if c.getJSON("/lol-gameflow/v1/gameflow-phase", &phase) == nil && phase == "Matchmaking" {
		c.do("DELETE", "/lol-lobby/v2/lobby/matchmaking/search", nil)
	}
	writeJSON(w, map[string]any{"ok": true})
}

// debugRaw gibt die Roh-Antwort eines LCU-Endpunkts zurück (nur für Fehlersuche).
func (a *App) debugRaw(w http.ResponseWriter, r *http.Request, path string) bool {
	c := a.getClient()
	if c == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "keine Verbindung zum League-Client"})
		return false
	}
	raw, err := c.do("GET", path, nil)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return false
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(raw)
	return true
}

func (a *App) debugFriendsHandler(w http.ResponseWriter, r *http.Request)  { a.debugRaw(w, r, "/lol-chat/v1/friends") }
func (a *App) debugSummonerHandler(w http.ResponseWriter, r *http.Request) { a.debugRaw(w, r, "/lol-summoner/v1/current-summoner") }
func (a *App) debugHistoryHandler(w http.ResponseWriter, r *http.Request) {
	c := a.getClient()
	if c == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "keine Verbindung zum League-Client"})
		return
	}
	if hp, ok := firstWorkingHistoryPath(c); ok {
		a.debugRaw(w, r, hp+"/matches?begIndex=0&endIndex=5")
		return
	}
	cands := matchHistoryCandidates(c)
	results := map[string]string{}
	for _, base := range cands {
		_, err := c.do("GET", base+"/matches?begIndex=0&endIndex=5", nil)
		if err != nil {
			results[base] = err.Error()
		} else {
			results[base] = "HTTP 200, aber leer/keine Spiele"
		}
	}
	writeJSON(w, map[string]any{"ok": false, "error": "kein funktionierender History-Pfad", "attempts": results})
}

// --- Main ---

// defaultAllowedOrigin ist die offizielle WebUI-Adresse. Sie ist
// standardmäßig erlaubt, damit die Bridge per Doppelklick (ohne Flags)
// mit der WebUI unter https://lolshards.pandasec.de zusammenarbeitet.
const defaultAllowedOrigin = "https://lolshards.pandasec.de"

// webUIURL wird nach dem Start automatisch im Standardbrowser geöffnet.
const webUIURL = "https://lolshards.pandasec.de"

// version wird beim Start angezeigt und in bridge.log geschrieben.
const version = "1.2.2"

var logFile *os.File
var logPath string

func openLog() {
	logPath = "bridge.log"
	if d, err := os.Executable(); err == nil {
		logPath = filepath.Join(filepath.Dir(d), "bridge.log")
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		logFile = f
	}
}

func say(args ...any) {
	fmt.Println(args...)
	if logFile != nil {
		fmt.Fprintln(logFile, args...)
	}
}

func sayf(format string, args ...any) {
	say(fmt.Sprintf(format, args...))
}

func main() {
	enableConsole()
	openLog()
	say("LoL-Shard-Bridge v" + version)
	if logFile != nil {
		say("Log wird mitgeschrieben: " + logPath)
	}
	defer func() {
		if r := recover(); r != nil {
			say("FATAL:", r)
			time.Sleep(10 * time.Second)
		}
	}()
	flagPort := flag.Int("port", 8700, "WebUI-Port (default 8700)")
	flagKey := flag.String("key", "", "optionaler Zugangsschlüssel für entfernte Frontends")
	flagOrigins := flag.String("allow-origin", "", "kommagetrennte erlaubte Ursprünge (z. B. https://meinseite.de)")
	flagNoBrowser := flag.Bool("no-browser", false, "Browser nicht automatisch öffnen")
	flagOpen := flag.String("open", "", "welche URL der Browser öffnen soll (Standard: die WebUI)")
	flag.Parse()

	// Auf den League-Client warten (Doppelklick-freundlich): kein sofortiger
	// Abbruch, sondern Meldung + erneuter Versuch, bis LoL läuft und eingeloggt ist.
	say("Suche League-Client ... bitte warten.")
	var client *Client
	startWait := time.Now()
	for attempt := 1; ; attempt++ {
		if lf := findLockfile(); lf != nil {
			c := NewClient(lf.Port, lf.Token)
			if err := c.getJSON("/lol-summoner/v1/current-summoner", &map[string]any{}); err == nil {
				client = c
				break
			}
		}
		if attempt%10 == 0 {
			ellapsed := int(time.Since(startWait).Seconds())
			sayf("Client noch nicht bereit (%ds) – ist LoL gestartet und eingeloggt?", ellapsed)
		}
		if time.Since(startWait) > 120*time.Second {
			say("")
			say("Kein League-Client gefunden.")
			say("Starte zuerst League of Legends und logge dich ein,")
			say("danach bridge.exe noch einmal starten.")
			say("Das Fenster schließt sich in 10 Sekunden.")
			time.Sleep(10 * time.Second)
			os.Exit(1)
		}
		time.Sleep(time.Second)
	}
	say("Verbunden mit dem LoL-Client (Port", client.Port(), ").")

	service := NewService(client)
	origins := []string{defaultAllowedOrigin}
	if *flagOrigins != "" {
		for _, o := range strings.Split(*flagOrigins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				origins = append(origins, o)
			}
		}
	}
	app := &App{
		client:   client,
		service:  service,
		hub:      newHub(),
		imgCache: map[string]imgEntry{},
		key:      *flagKey,
		origins:  origins,
	}

	go app.watchdog()
	go app.poller()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Die Web-UI liegt remote – direkter Aufruf der Bridge leitet dorthin.
		http.Redirect(w, r, webUIURL, http.StatusFound)
	})
	mux.HandleFunc("/api/ping", app.corsAndGuard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, `{"ok":true,"bridge":"go"}`)
	}))
	mux.HandleFunc("/api/loot", app.corsAndGuard(app.lootHandler))
	mux.HandleFunc("/api/craft", app.corsAndGuard(app.craftHandler))
	mux.HandleFunc("/api/img", app.corsAndGuard(app.imgHandler))
	mux.HandleFunc("/api/stream", app.corsAndGuard(app.streamHandler))
	mux.HandleFunc("/api/client", app.corsAndGuard(app.clientHandler))
	mux.HandleFunc("/api/collections", app.corsAndGuard(app.collectionsHandler))
	mux.HandleFunc("/api/play", app.corsAndGuard(app.playHandler))
	mux.HandleFunc("/api/cancel", app.corsAndGuard(app.cancelHandler))
	mux.HandleFunc("/api/debug/friends", app.corsAndGuard(app.debugFriendsHandler))
	mux.HandleFunc("/api/debug/summoner", app.corsAndGuard(app.debugSummonerHandler))
	mux.HandleFunc("/api/debug/history", app.corsAndGuard(app.debugHistoryHandler))

	addr := fmt.Sprintf("127.0.0.1:%d", *flagPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		say("")
		sayf("FEHLER: Port %d ist bereits belegt.", *flagPort)
		say("Es läuft vermutlich schon eine alte bridge.exe.")
		say("Schließe alle bridge.exe-Prozesse (Task-Manager) und starte die Bridge neu.")
		say("Das Fenster schließt sich in 15 Sekunden.")
		time.Sleep(15 * time.Second)
		os.Exit(1)
	}
	srv := &http.Server{Handler: mux}
	sayf("Verbindung zum LoL-Client hergestellt (Port %d).", client.Port())
	log.Printf("Verbindung zum LoL-Client hergestellt (Port %d).", client.Port())
	sayf("WebUI läuft: http://%s/", addr)
	if *flagKey != "" {
		log.Printf("Entfernte Frontends müssen den X-UI-Key-Header senden.")
	}
	if !*flagNoBrowser {
		go func() {
			time.Sleep(400 * time.Millisecond)
			target := webUIURL
			if *flagOpen != "" {
				target = *flagOpen
			}
			openBrowser(target)
		}()
	}
	log.Printf("Zum Beenden: Strg+C")
	printReady()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		say("FATAL:", err)
		time.Sleep(10 * time.Second)
	}
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}

// clearConsole leert den Konsolenbildschirm (plattformspezifisch,
// console_windows.go / console_other.go), so dass nur das Banner stehen bleibt.

// printReady zeigt ein ASCII-Art-Banner, sobald die Bridge fertig ist.
func printReady() {
	clearConsole()
	art := `+==============================================+
|                                              |
| ██████╗ ██████╗ ██╗██████╗  ██████╗ ███████╗ |
| ██╔══██╗██╔══██╗██║██╔══██╗██╔════╝ ██╔════╝ |
| ██████╔╝██████╔╝██║██║  ██║██║  ███╗█████╗   |
| ██╔══██╗██╔══██╗██║██║  ██║██║   ██║██╔══╝   |
| ██████╔╝██║  ██║██║██████╔╝╚██████╔╝███████╗ |
| ╚═════╝ ╚═╝  ╚═╝╚═╝╚═════╝  ╚═════╝ ╚══════╝ |
|                                              |
| ██████╗ ███████╗ █████╗ ██████╗ ██╗   ██╗    |
| ██╔══██╗██╔════╝██╔══██╗██╔══██╗╚██╗ ██╔╝    |
| ██████╔╝█████╗  ███████║██║  ██║ ╚████╔╝     |
| ██╔══██╗██╔══╝  ██╔══██║██║  ██║  ╚██╔╝      |
| ██║  ██║███████╗██║  ██║██████╔╝   ██║       |
| ╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚═════╝    ╚═╝       |
|                                              |
+==============================================+`
	for _, l := range strings.Split(art, "\n") {
		say(l)
	}
	say("")
	say("LoL-Shard-Bridge v" + version + " · BEREIT – WebUI wird geöffnet: " + webUIURL)
}
