package operator

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

var (
	cachedIp      = ""
	cachedIpMutex = sync.Mutex{}
	cachedIpTtl   = time.Duration(5 * time.Minute)
	cachedIpTime  = time.Now().Add(-cachedIpTtl)
)

// Gets the public IP attached to this workload
// Returns an error if fetching the public IP fails.
func getPublicIp() (string, error) {
	r, err := http.Get("http://whatismyip.akamai.com")
	if err != nil {
		return "", err
	}
	if r.StatusCode != 200 {
		return "", fmt.Errorf("request to %s failed with status code %d", r.Request.URL, r.StatusCode)
	}
	ipb, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	ip := string(ipb)
	return ip, nil
}

// Gets the (cached) public IP attached to this workload.
// Returns an error if fetching the public IP fails.
func GetPublicIp() (string, error) {
	cachedIpMutex.Lock()
	defer cachedIpMutex.Unlock()

	n := time.Now()
	if n.Sub(cachedIpTime) >= cachedIpTtl {
		ip, err := getPublicIp()
		if err != nil {
			return "", err
		}
		cachedIp = ip
		cachedIpTime = n
	}
	return cachedIp, nil
}
