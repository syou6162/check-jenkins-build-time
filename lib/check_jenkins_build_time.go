package checkjenkinsbuildtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jessevdk/go-flags"
	"github.com/mackerelio/checkers"
)

var opts struct {
	Scheme        string `short:"s" long:"scheme" default:"http" description:"Jenkins scheme"`
	Host          string `short:"h" long:"host" default:"localhost" description:"Jenkins hostname"`
	Port          int64  `short:"p" long:"port" default:"8080" description:"Jenkins port"`
	JobName       string `short:"j" long:"job-name" required:"true" description:"Monitor job name"`
	MaxJobNumber  int64  `long:"max-job-number" default:"10" description:"Number of recent jobs to monitor"`
	WarningSecond int64  `short:"w" long:"warning-second" default:"60" description:"Trigger a warning if over the seconds"`
	CritSecond    int64  `short:"c" long:"critical-second" default:"300" description:"Trigger a critical if over the seconds"`
}

/*
Jenkins api result contains build millisecond timestamp like this.

% curl -s --globoff "http://localhost:8080/job/sleep30/api/json?tree=builds[result,number,timestamp,status]{,2}" | jq .
{
  "_class": "hudson.model.FreeStyleProject",
  "builds": [
    {
      "_class": "hudson.model.FreeStyleBuild",
      "number": 57,
      "result": null,
      "timestamp": 1503146442652
    },
    {
      "_class": "hudson.model.FreeStyleBuild",
      "number": 51,
      "result": "SUCCESS",
      "timestamp": 1503144132413
    }
  ]
}

To decode the above format json, we define `jsonTime`.
*/

type jsonTime time.Time

func (t jsonTime) toTime() time.Time { return time.Time(t) }
func (t jsonTime) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(t.toTime().Unix(), 10)), nil
}

func (t *jsonTime) UnmarshalJSON(s []byte) (err error) {
	r := strings.Replace(string(s), `"`, ``, -1)

	q, err := strconv.ParseInt(r, 10, 64)
	if err != nil {
		return err
	}
	*(*time.Time)(t) = time.Unix(q/1000, 0)
	return
}

func (t jsonTime) String() string { return t.toTime().String() }

type build struct {
	Number    int      `json:"number"`
	Result    *string  `json:"result"`
	Timestamp jsonTime `json:"timestamp"`
}

func (b build) isUnfinished() bool {
	return b.Result == nil
}

type builds struct {
	Builds []build `json:"builds"`
}

// Do the plugin
func Do() {
	ckr := run(os.Args[1:])
	ckr.Name = "JenkinsBuildTime"
	ckr.Exit()
}

func filterUnfinishedTooLongBuilds(builds []build, threshold time.Duration) []build {
	now := time.Now()
	ret := make([]build, 0)

	for _, b := range builds {
		if b.isUnfinished() && now.Sub(b.Timestamp.toTime()) > threshold {
			ret = append(ret, b)
		}
	}
	return ret
}

func run(args []string) *checkers.Checker {
	_, err := flags.ParseArgs(&opts, args)
	if err != nil {
		os.Exit(1)
	}

	// Jenkins does not provide api to get recent builds that does not finished yet.
	// Instead, we check recent `MaxJobNumber` jobs, and filter unfinished and taking too long time jobs
	url := fmt.Sprintf("%s://%s:%d/job/%s/api/json?tree=builds[result,number,timestamp]{,%d}", opts.Scheme, opts.Host, opts.Port, opts.JobName, opts.MaxJobNumber)
	resp, err := http.Get(url)

	if err != nil {
		return checkers.Unknown(fmt.Sprintf("Faild to fetch jenkins metrics: %s", err))
	}
	defer resp.Body.Close()
	var builds builds

	json.NewDecoder(resp.Body).Decode(&builds)

	checkSt := checkers.OK

	for _, b := range filterUnfinishedTooLongBuilds(builds.Builds, time.Second*time.Duration(opts.CritSecond)) {
		checkSt = checkers.CRITICAL
		msg := fmt.Sprintf("Build id = %d takes too long time", b.Number)
		return checkers.NewChecker(checkSt, msg)
	}

	for _, b := range filterUnfinishedTooLongBuilds(builds.Builds, time.Second*time.Duration(opts.WarningSecond)) {
		checkSt = checkers.WARNING
		msg := fmt.Sprintf("Build id = %d takes too long time", b.Number)
		return checkers.NewChecker(checkSt, msg)
	}
	return checkers.NewChecker(checkSt, "No build that takes too long time exists")
}