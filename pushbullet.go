// Package pushbullet provides simple access to the v2 API of http://pushbullet.com.
/*

Example client:
	pb := pushbullet.New("YOUR_API_KEY")
	devices, err := pb.Devices()
	...
	err = pb.PushNote(devices[0].Iden, "Hello!", "Hi from go-pushbullet!")

The API is document at https://docs.pushbullet.com/http/ .  At the moment, it only supports querying devices and sending notifications.

*/
package pushbullet

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
)

// EndpointURL sets the default URL for the Pushbullet API
var EndpointURL = "https://api.pushbullet.com/v2"

// Endpoint allows manipulation of pushbullet API endpoint for testing
type Endpoint struct {
	URL string
}

// A Client connects to PushBullet with an API Key.
type Client struct {
	Key    string
	Client *http.Client
	Endpoint
}

// New creates a new client with your personal API key.
func New(apikey string) *Client {
	endpoint := Endpoint{URL: EndpointURL}
	return &Client{apikey, http.DefaultClient, endpoint}
}

// NewWithClient creates a new client with your personal API key and the given http Client
func NewWithClient(apikey string, client *http.Client) *Client {
	endpoint := Endpoint{URL: EndpointURL}
	return &Client{apikey, client, endpoint}
}

// A Device is a PushBullet device
type Device struct {
	Iden              string  `json:"iden"`
	Active            bool    `json:"active"`
	Created           float32 `json:"created"`
	Modified          float32 `json:"modified"`
	Icon              string  `json:"icon"`
	Nickname          string  `json:"nickname"`
	GeneratedNickname string  `json:"generated_nickname"`
	Manufacturer      string  `json:"manufacturer"`
	Model             string  `json:"model"`
	AppVersion        int     `json:"app_version"`
	Fingerprint       string  `json:"fingerprint"`
	KeyFingerprint    string  `json:"key_fingerprint"`
	PushToken         string  `json:"push_token"`
	HasSms            string  `json:"has_sms"`
	Client            *Client `json:"-"`
}

// ErrResponse is an error returned by the PushBullet API
type ErrResponse struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Cat     string `json:"cat"`
}

func (e *ErrResponse) Error() string {
	return e.Message
}

type errorResponse struct {
	ErrResponse `json:"error"`
}

type deviceResponse struct {
	Devices       []*Device
	SharedDevices []*Device `json:"shared_devices"`
}

func (c *Client) buildRequest(object string, data interface{}) *http.Request {
	r, err := http.NewRequest("GET", c.Endpoint.URL+object, nil)
	if err != nil {
		panic(err)
	}

	// appengine sdk requires us to set the auth header by hand
	u := url.UserPassword(c.Key, "")
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.String())))

	if data != nil {
		r.Method = "POST"
		r.Header.Set("Content-Type", "application/json")
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.Encode(data)
		r.Body = ioutil.NopCloser(&b)
	}

	return r
}

// Devices fetches a list of devices from PushBullet.
func (c *Client) Devices() ([]*Device, error) {
	req := c.buildRequest("/devices", nil)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errjson errorResponse
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&errjson)
		if err == nil {
			return nil, &errjson.ErrResponse
		}

		return nil, errors.New(resp.Status)
	}

	var devResp deviceResponse
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&devResp)
	if err != nil {
		return nil, err
	}

	for i := range devResp.Devices {
		devResp.Devices[i].Client = c
	}
	devices := append(devResp.Devices, devResp.SharedDevices...)
	return devices, nil
}

// Device fetches an device with a given nickname from PushBullet.
func (c *Client) Device(nickname string) (*Device, error) {
	devices, err := c.Devices()
	if err != nil {
		return nil, err
	}

	for i := range devices {
		if devices[i].Nickname == nickname {
			devices[i].Client = c
			return devices[i], nil
		}
	}
	return nil, errors.New("Device not found!")
}

// PushNote sends a note to the specific device with the given title and body
func (d *Device) PushNote(title, body string) error {
	data := Note{
		Iden:  d.Iden,
		Type:  "note",
		Title: title,
		Body:  body,
	}
	return d.Client.Push("/pushes", data)
}

// PushLink sends a link to the specific device with the given title and url
func (d *Device) PushLink(title, u, body string) error {
	data := Link{
		Iden:  d.Iden,
		Type:  "link",
		Title: title,
		URL:   u,
		Body:  body,
	}
	return d.Client.Push("/pushes", data)
}

// PushSMS sends an SMS to the specific user from the device with the given title and url
func (d *Device) PushSMS(deviceIden, phoneNumber, message string) error {
	data := Ephemeral{
		Type: "push",
		Push: EphemeralPush{
			Type:             "messaging_extension_reply",
			PackageName:      "com.pushbullet.android",
			SourceUserIden:   d.Iden,
			TargetDeviceIden: deviceIden,
			ConversationIden: phoneNumber,
			Message:          message,
		},
	}
	return d.Client.Push("/ephemerals", data)
}

// User represents the User object for pushbullet
type User struct {
	Iden            string      `json:"iden"`
	Email           string      `json:"email"`
	EmailNormalized string      `json:"email_normalized"`
	Created         float64     `json:"created"`
	Modified        float64     `json:"modified"`
	Name            string      `json:"name"`
	ImageUrl        string      `json:"image_url"`
	Preferences     interface{} `json:"preferences"`
}

// Me returns the user object for the pushbullet user
func (c *Client) Me() (*User, error) {
	req := c.buildRequest("/users/me", nil)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errjson errorResponse
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&errjson)
		if err == nil {
			return nil, &errjson.ErrResponse
		}

		return nil, errors.New(resp.Status)
	}

	var userResponse User
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&userResponse)
	if err != nil {
		return nil, err
	}
	return &userResponse, nil
}

// Push pushes the data to a specific device registered with PushBullet.  The
// 'data' parameter is marshaled to JSON and sent as the request body.  Most
// users should call one of PusNote, PushLink, PushAddress, or PushList.
func (c *Client) Push(endPoint string, data interface{}) error {
	req := c.buildRequest(endPoint, data)
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResponse errorResponse
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&errResponse)
		if err == nil {
			return &errResponse.ErrResponse
		}

		return errors.New(resp.Status)
	}

	return nil
}

// Note exposes the required and optional fields of the Pushbullet push type=note
type Note struct {
	Iden  string `json:"device_iden,omitempty"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// PushNote pushes a note with title and body to a specific PushBullet device.
func (c *Client) PushNote(iden string, title, body string) error {
	data := Note{
		Iden:  iden,
		Type:  "note",
		Title: title,
		Body:  body,
	}
	return c.Push("/pushes", data)
}

// Link exposes the required and optional fields of the Pushbullet push type=link
type Link struct {
	Iden  string `json:"device_iden"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Body  string `json:"body,omitempty"`
}

// PushLink pushes a link with a title and url to a specific PushBullet device.
func (c *Client) PushLink(iden, title, u, body string) error {
	data := Link{
		Iden:  iden,
		Type:  "link",
		Title: title,
		URL:   u,
		Body:  body,
	}
	return c.Push("/pushes", data)
}

// EphemeralPush  exposes the required fields of the Pushbullet ephemeral object
type EphemeralPush struct {
	Type             string `json:"type"`
	PackageName      string `json:"package_name"`
	SourceUserIden   string `json:"source_user_iden"`
	TargetDeviceIden string `json:"target_device_iden"`
	ConversationIden string `json:"conversation_iden"`
	Message          string `json:"message"`
}

// Ephemeral constructs the Ephemeral object for pushing which requires the EphemeralPush object
type Ephemeral struct {
	Type string        `json:"type"`
	Push EphemeralPush `json:"push"`
}

// PushSMS sends an SMS message with pushbullet
func (c *Client) PushSMS(userIden, deviceIden, phoneNumber, message string) error {
	data := Ephemeral{
		Type: "push",
		Push: EphemeralPush{
			Type:             "messaging_extension_reply",
			PackageName:      "com.pushbullet.android",
			SourceUserIden:   userIden,
			TargetDeviceIden: deviceIden,
			ConversationIden: phoneNumber,
			Message:          message,
		},
	}
	return c.Push("/ephemerals", data)
}
