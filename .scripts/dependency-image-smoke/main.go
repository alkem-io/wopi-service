// Command smoke-fixture exercises the final WOPI executable with synthetic peers.
package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const fileID = "11111111-1111-4111-8111-111111111111"
const actorID = "22222222-2222-4222-8222-222222222222"
const content = "synthetic WOPI file\n"

var client = &http.Client{Timeout: 10 * time.Second}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "broker-ready" {
		conn, err := amqp.Dial("amqp://smoke:smoke@broker:5672/")
		if err != nil {
			log.Fatal(err)
		}
		_ = conn.Close()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "ready" {
		resp, err := client.Get("http://wopi:8080/health")
		if err != nil {
			log.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "check" {
		// The aggregate covers every sequential request plus the broker poll, so it gets its own longer budget.
		checkClient := &http.Client{Timeout: 60 * time.Second}
		resp, err := checkClient.Get("http://127.0.0.1:8090/test")
		if err != nil {
			log.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(string(body))
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") && r.ProtoMajor != 2 {
			http.Error(w, "internal peers require h2c", http.StatusHTTPVersionNotSupported)
			return
		}
		switch r.URL.Path {
		case "/hosting/discovery":
			fmt.Fprintf(w, `<wopi-discovery><net-zone name="external-http"><app name="writer"><action name="edit" ext="docx" urlsrc="http://fixture:8090/browser/edit?"/></app></net-zone><proof-key modulus="%s" exponent="%s"/></wopi-discovery>`, base64.StdEncoding.EncodeToString(key.N.Bytes()), base64.StdEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()))
		case "/internal/file/" + fileID + "/meta":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": fileID, "externalID": "private-storage-version", "mimeType": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "size": len(content), "displayName": "smoke.docx", "createdBy": "owner", "authorizationId": "policy", "updatedDate": "2026-01-01T00:00:00Z"})
		case "/internal/file/" + fileID + "/content":
			_, _ = io.WriteString(w, content)
		case "/internal/auth/evaluate":
			var request struct {
				ActorID   string `json:"actorId"`
				Privilege string `json:"privilege"`
				Policy    string `json:"authorizationPolicyId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			allowed := request.ActorID == actorID && request.Policy == "policy" && (request.Privilege == "read" || request.Privilege == "update-content")
			_ = json.NewEncoder(w).Encode(map[string]bool{"allowed": allowed})
		case "/test":
			if err := exercise(key); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			_, _ = io.WriteString(w, "PASS token issuance; h2c authorization and file identity; signed CheckFileInfo/GetFile; token/proof/auth rejection; durable NestJS rename envelope\n")
		default:
			http.NotFound(w, r)
		}
	})
	srv := &http.Server{Addr: ":8090", Handler: h2c.NewHandler(handler, &http2.Server{}), ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func request(method, path, body string, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequest(method, "http://wopi:8080"+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func proof(key *rsa.PrivateKey, path, token string) (map[string]string, error) {
	ticks := time.Now().UnixNano()/100 + 621355968000000000
	var payload bytes.Buffer
	for _, part := range [][]byte{[]byte(token), []byte(strings.ToUpper("http://wopi:8080" + path))} {
		if err := binary.Write(&payload, binary.BigEndian, uint32(len(part))); err != nil {
			return nil, err
		}
		_, _ = payload.Write(part)
	}
	if err := binary.Write(&payload, binary.BigEndian, uint32(8)); err != nil {
		return nil, err
	}
	if err := binary.Write(&payload, binary.BigEndian, ticks); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(payload.Bytes())
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		return nil, err
	}
	return map[string]string{"X-WOPI-Proof": base64.StdEncoding.EncodeToString(signature), "X-WOPI-TimeStamp": fmt.Sprint(ticks)}, nil
}

func exercise(key *rsa.PrivateKey) error {
	status, data, err := request("POST", "/wopi/token", `{"documentId":"`+fileID+`","actorName":"Synthetic Actor"}`, map[string]string{"X-Alkemio-Actor-Id": actorID})
	if err != nil || status != 200 {
		return fmt.Errorf("issue token: %d %s %w", status, data, err)
	}
	var issued struct {
		AccessToken string `json:"accessToken"`
		WOPISrc     string `json:"wopiSrc"`
	}
	if err := json.Unmarshal(data, &issued); err != nil {
		return err
	}
	if issued.AccessToken == "" || issued.WOPISrc != "http://wopi:8080/wopi/files/"+fileID {
		return fmt.Errorf("token identity contract: %s", data)
	}
	path := "/wopi/files/" + fileID + "?access_token=" + issued.AccessToken
	headers, err := proof(key, path, issued.AccessToken)
	if err != nil {
		return err
	}
	status, data, err = request("GET", path, "", headers)
	if err != nil || status != 200 {
		return fmt.Errorf("CheckFileInfo: %d %s %w", status, data, err)
	}
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		return err
	}
	if info["BaseFileName"] != "smoke.docx" || info["UserId"] != actorID || info["OwnerId"] != "owner" || info["UserCanWrite"] != true || info["Size"] != float64(len(content)) {
		return fmt.Errorf("CheckFileInfo contract: %s", data)
	}
	contentPath := "/wopi/files/" + fileID + "/contents?access_token=" + issued.AccessToken
	headers, err = proof(key, contentPath, issued.AccessToken)
	if err != nil {
		return err
	}
	status, data, err = request("GET", contentPath, "", headers)
	if err != nil || status != 200 || string(data) != content {
		return fmt.Errorf("GetFile: %d %q %w", status, data, err)
	}
	for _, bad := range []struct {
		path    string
		headers map[string]string
		body    string
	}{
		{path: path, headers: map[string]string{"X-WOPI-Proof": "AAAA", "X-WOPI-TimeStamp": fmt.Sprint(time.Now().UnixNano()/100 + 621355968000000000)}, body: "invalid proof"},
		{path: "/wopi/files/" + fileID + "?access_token=invalid", body: "invalid or expired token"},
	} {
		status, data, err = request("GET", bad.path, "", bad.headers)
		if err != nil || status != 401 || !strings.Contains(string(data), bad.body) {
			return fmt.Errorf("rejection: %d %s %w", status, data, err)
		}
	}
	status, _, err = request("POST", "/wopi/token", `{"documentId":"`+fileID+`"}`, nil)
	if err != nil || status != 401 {
		return fmt.Errorf("missing identity rejection: %d %w", status, err)
	}
	status, _, err = request("POST", "/wopi/token", `{"documentId":"`+fileID+`"}`, map[string]string{"X-Alkemio-Actor-Id": "denied-actor"})
	if err != nil || status != 403 {
		return fmt.Errorf("authorization rejection: %d %w", status, err)
	}
	headers, err = proof(key, path, issued.AccessToken)
	if err != nil {
		return err
	}
	headers["X-WOPI-Override"] = "RENAME_FILE"
	headers["X-WOPI-RequestedName"] = "renamed"
	status, data, err = request("POST", path, "", headers)
	if err != nil || status != 200 {
		return fmt.Errorf("RenameFile: %d %s %w", status, data, err)
	}
	conn, err := amqp.Dial("amqp://smoke:smoke@broker:5672/")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()
	for range 30 {
		msg, ok, err := ch.Get("collaboration-document-service", true)
		if err != nil {
			return err
		}
		if ok {
			var envelope struct {
				Pattern string `json:"pattern"`
				Data    struct {
					DocumentID  string `json:"documentId"`
					DisplayName string `json:"displayName"`
				} `json:"data"`
			}
			if err := json.Unmarshal(msg.Body, &envelope); err != nil {
				return err
			}
			if envelope.Pattern != "collaboration-office-document-rename" || envelope.Data.DocumentID != fileID || envelope.Data.DisplayName != "renamed" || msg.DeliveryMode != amqp.Persistent || msg.ContentType != "application/json" {
				return fmt.Errorf("broker envelope mismatch: %s", msg.Body)
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("rename event absent from real broker")
}
