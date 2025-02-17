package buildtest

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	common "github.com/commonjava/indy-tests/pkg/common"
)

const (
	TMP_DOWNLOAD_DIR = "/tmp/download"
	TMP_UPLOAD_DIR   = "/tmp/upload"
	PROXY_           = "proxy-"
)

const DATA_TIME = "2006-01-02 15:04:05"

func Run(originalIndy, foloId, replacement, targetIndy, packageType string, processNum int) {
	origIndy := originalIndy
	if !strings.HasPrefix(origIndy, "http://") {
		origIndy = "http://" + origIndy
	}
	foloTrackContent := common.GetFoloRecord(origIndy, foloId)
	newBuildName := common.GenerateRandomBuildName()
	DoRun(originalIndy, targetIndy, "", "", packageType, newBuildName, foloTrackContent, nil, processNum, false, false)
}

// Create the repo structure and do the download/upload
func DoRun(originalIndy, targetIndy, indyProxyUrl, migrateTargetIndy, packageType, newBuildName string, foloTrackContent common.TrackedContent,
	additionalRepos []string,
	processNum int, clearCache, dryRun bool) bool {

	common.ValidateTargetIndyOrExit(originalIndy)
	targetIndyHost, _ := common.ValidateTargetIndyOrExit(targetIndy)

	migrateEnabled := (migrateTargetIndy != "")
	if migrateEnabled {
		migrateTargetIndyHost, _ := common.ValidateTargetIndyOrExit(migrateTargetIndy)
		fmt.Printf("Migrate to host %s", migrateTargetIndyHost)
	}
	// Prepare the indy repos for the whole testing
	buildMeta := decideMeta(packageType)
	if !prepareIndyRepos("http://"+targetIndyHost, newBuildName, *buildMeta, additionalRepos, dryRun) {
		os.Exit(1)
	}

	trackingId := foloTrackContent.TrackingKey.Id
	downloadDir, uploadDir := prepareDownUploadDirectories(trackingId, clearCache)

	proxyEnabled := (indyProxyUrl != "")
	downloads := prepareDownloadEntriesByFolo(targetIndy, newBuildName, packageType, foloTrackContent, additionalRepos, proxyEnabled)
	defer cleanGenericProxyReposIfAny(targetIndy, newBuildName, foloTrackContent, proxyEnabled)

	downloadFunc := func(md5str, originalArtiURL, targetArtiURL string) bool {
		fileLoc := path.Join(downloadDir, path.Base(targetArtiURL))
		if dryRun {
			fmt.Printf("Dry run download, url: %s\n", targetArtiURL)
			return true
		}
		success := false
		if strings.HasPrefix(targetArtiURL, PROXY_) {
			success = common.DownloadFileByProxy(targetArtiURL[len(PROXY_):], fileLoc, indyProxyUrl, newBuildName+common.TRACKING_SUFFIX, "pass")
		} else {
			success, _ = common.DownloadFile(targetArtiURL, fileLoc)
		}
		if success {
			common.Md5Check(fileLoc, md5str)
		}
		return success
	}

	migrateFunc := func(md5str, targetArtiURL, migrateTargetArtiURL string) bool {
		fileLoc := path.Join(downloadDir, path.Base(targetArtiURL))
		if dryRun {
			fmt.Printf("Dry run download, url: %s\n", targetArtiURL)
			return true
		}
		success := false
		success, _ = common.DownloadFile(targetArtiURL, fileLoc)
		if success {
			common.Md5Check(fileLoc, md5str)
			if dryRun {
				fmt.Printf("Dry run upload, url: %s\n", migrateTargetArtiURL)
				return true
			}
			common.UploadFile(migrateTargetArtiURL, fileLoc)
		}
		return success
	}

	broken := false

	if migrateEnabled {
		migrateTargetIndyHost, _ := common.ValidateTargetIndyOrExit(migrateTargetIndy)
		migrateArtifacts := prepareMigrateEntriesByFolo(targetIndy, migrateTargetIndyHost, packageType, newBuildName, foloTrackContent)
		fmt.Printf("Waiting 60s...\n")
		time.Sleep(120 * time.Second) // wait for Indy event handled
		for _, down := range migrateArtifacts {
			broken = !migrateFunc(down[0], down[1], down[2])
			if broken {
				break
			}
		}
		fmt.Println("==========================================")
		if broken {
			fmt.Printf("Build test failed due to some downloading errors. Please see above logs to see the details.\n\n")
			os.Exit(1)
		}
		fmt.Printf("Migration artifacts handling finished.\n\n")
		return true
	}

	if len(downloads) > 0 && !migrateEnabled {
		fmt.Println("Start handling downloads artifacts.")
		fmt.Printf("==========================================\n\n")
		if processNum > 1 {
			broken = !common.ConcurrentRun(processNum, downloads, downloadFunc)
		} else {
			for _, down := range downloads {
				broken = !downloadFunc(down[0], down[1], down[2])
				if broken {
					break
				}
			}
		}
		fmt.Println("==========================================")
		if broken {
			fmt.Printf("Build test failed due to some downloading errors. Please see above logs to see the details.\n\n")
			os.Exit(1)
		}
		fmt.Printf("Downloads artifacts handling finished.\n\n")
	}

	uploadFunc := func(md5str, originalArtiURL, targetArtiURL string) bool {
		if dryRun {
			fmt.Printf("Dry run upload, originalArtiURL: %s, targetArtiURL: %s\n", originalArtiURL, targetArtiURL)
			return true
		}

		cacheFile := path.Join(uploadDir, path.Base(originalArtiURL))
		var downloaded bool
		if common.FileOrDirExists(cacheFile) {
			fmt.Printf("File already downloaded, reuse cacheFile: %s\n", cacheFile)
			downloaded = true
		} else {
			downloaded = common.DownloadUploadFileForCache(originalArtiURL, cacheFile)
		}
		if downloaded {
			common.Md5Check(cacheFile, md5str)
			return common.UploadFile(targetArtiURL, cacheFile)
		}
		return false
	}

	uploads := prepareUploadEntriesByFolo(originalIndy, targetIndy, newBuildName, foloTrackContent)

	if len(uploads) > 0 && !migrateEnabled {
		fmt.Println("Start handling uploads artifacts.")
		fmt.Printf("==========================================\n\n")
		if processNum > 1 {
			broken = !common.ConcurrentRun(processNum, uploads, uploadFunc)
		} else {
			for _, up := range uploads {
				broken = !uploadFunc(up[0], up[1], up[2])
				if broken {
					break
				}
			}
		}
		fmt.Println("==========================================")
		if broken {
			fmt.Printf("Build test failed due to some uploadig errors. Please see above logs to see the details.\n\n")
			os.Exit(1)
		}

		fmt.Printf("Uploads artifacts handling finished.\n\n")
	}
	if !broken && !dryRun {
		targIndy := targetIndy
		if !strings.HasPrefix(targIndy, "http://") {
			targIndy = "http://" + targIndy
		}
		if common.SealFoloRecord(targIndy, newBuildName) {
			fmt.Printf("Folo record sealing succeeded for %s\n", newBuildName)
		} else {
			fmt.Printf("Warning: folo record sealing failed for %s\n", newBuildName)
		}
	}

	return true
}

// Remove the repositories that were generated by httproxy. We need to clean them after the test.
func cleanGenericProxyReposIfAny(indyBaseUrl, newBuildId string, foloRecord common.TrackedContent, proxyEnabled bool) {
	if !proxyEnabled {
		fmt.Println("No generic proxy repos to clean up. Proxy not enabled.")
		return
	}
	fmt.Println("Clean up generic proxy repos.")
	for _, down := range foloRecord.Downloads {
		if down.AccessChannel == "GENERIC_PROXY" {
			host := getRepoNameByOriginUrl(down.OriginUrl)
			packageType := "generic-http"
			deleteIndyHosted(indyBaseUrl, packageType, "h-"+host+"-"+newBuildId)
			deleteIndyRemote(indyBaseUrl, packageType, "r-"+host+"-"+newBuildId)
			deleteIndyGroup(indyBaseUrl, packageType, "g-"+host+"-"+newBuildId)
		}
	}
}

func getRepoNameByOriginUrl(originUrl string) string {
	u, _ := url.Parse(originUrl)
	host := u.Hostname()
	return strings.ReplaceAll(host, ".", "-")
}

// For downloads entries, we will get the paths and inject them to the final url of target indy
// as they should be directly download from target indy.
func prepareDownloadEntriesByFolo(targetIndyURL, newBuildId, packageType string,
	foloRecord common.TrackedContent, additionalRepos []string, proxyEnabled bool) map[string][]string {
	targetIndy := normIndyURL(targetIndyURL)
	result := make(map[string][]string)
	for _, down := range foloRecord.Downloads {
		var p string
		downUrl := ""
		repoPath := strings.ReplaceAll(down.StoreKey, ":", "/")
		if down.AccessChannel == "GENERIC_PROXY" {
			if proxyEnabled {
				downUrl = fmt.Sprintf("%s%s", PROXY_, down.OriginUrl)
			} else {
				// Generic remote repo may not be available when we replay the build. PNC has promoted the files to "h-" repo,
				// so we replace "generic-http/remote/r-xxxx" to "generic-http:hosted:h-xxxx"
				repoPath = strings.Replace(repoPath, "generic-http/remote/r-", "generic-http/hosted/h-", 1)
				p = path.Join("api/content", repoPath, down.Path)
				downUrl = fmt.Sprintf("%s%s", targetIndy, p)
			}
		} else {
			// To explain the 'HasPrefix': NPM build can have downloads from maven repos (or vice verse). We use the original repo
			// if the storeKey is not compliant with packageType
			if common.Contains(additionalRepos, down.StoreKey) || !strings.HasPrefix(down.StoreKey, packageType) {
				p = path.Join("api/folo/track", newBuildId, repoPath, down.Path)
			} else {
				p = path.Join("api/folo/track", newBuildId, packageType, "group", newBuildId, down.Path)
			}
			downUrl = fmt.Sprintf("%s%s", targetIndy, p)
		}
		result[down.Path] = []string{down.Md5, "", downUrl}
	}
	return result
}

func prepareMigrateEntriesByFolo(targetIndyURL, migrateTargetIndyHost, packageType,
	newBuildId string, foloRecord common.TrackedContent) map[string][]string {
	targetIndy := normIndyURL(targetIndyURL)
	result := make(map[string][]string)
	for _, down := range foloRecord.Downloads {
		var p string
		downUrl := ""
		repoPath := strings.ReplaceAll(down.StoreKey, ":", "/")
		if down.AccessChannel == "GENERIC_PROXY" {
			repoPath = strings.Replace(repoPath, "generic-http/remote/r-", "generic-http/hosted/h-", 1)
			p = path.Join("api/content", repoPath, down.Path)
		} else {
			if !strings.HasPrefix(down.StoreKey, packageType) {
				p = path.Join("api/content", repoPath, down.Path)
			} else {
				p = path.Join("api/content", packageType, "group", newBuildId, down.Path)
			}
		}

		downUrl = fmt.Sprintf("%s%s", targetIndy, p)

		broken := false
		migratePath := setHostname(down.LocalUrl, migrateTargetIndyHost)
		fmt.Printf("[%s] Deleting %s\n", time.Now().Format(DATA_TIME), migratePath)
		broken = !delArtifact(migratePath)
		time.Sleep(100 * time.Millisecond)

		if !strings.HasSuffix( down.StoreKey, ":hosted:shared-imports" ) {
			extra, _ := url.JoinPath("http://"+migrateTargetIndyHost, "/api/content", packageType, "/hosted/shared-imports", down.Path)
			fmt.Printf("[%s] Deleting %s\n", time.Now().Format(DATA_TIME), extra)
			broken = !delArtifact(extra)
			time.Sleep(100 * time.Millisecond)
		}

		if down.StoreKey == "npm:remote:npmjs" || down.StoreKey == "maven:remote:central" {
			migratePath, _ = url.JoinPath("http://"+migrateTargetIndyHost, "/api/content", packageType, "/hosted/shared-imports", down.Path)
			fmt.Printf("[%s] Deleting %s\n", time.Now().Format(DATA_TIME), migratePath)
			broken = !delArtifact(migratePath)
			time.Sleep(100 * time.Millisecond)
		} else if down.StoreKey == "maven:remote:mrrc-ga-rh" || strings.HasPrefix(down.StoreKey, "maven:hosted:build-") {
			migratePath, _ = url.JoinPath("http://"+migrateTargetIndyHost, "/api/content", packageType, "/hosted/pnc-builds", down.Path)
			fmt.Printf("[%s] Deleting %s\n", time.Now().Format(DATA_TIME), migratePath)
			broken = !delArtifact(migratePath)
			time.Sleep(100 * time.Millisecond)
		}

		if broken {
			fmt.Printf("[%s] Deletion failed for %s\n", time.Now().Format(DATA_TIME), migratePath)
		}

		result[down.Path] = []string{down.Md5, downUrl, migratePath}
	}
	return result
}

// For uploads entries, firstly they should be downloaded from original indy server. We use original indy server to
// make the download url, and use the target indy server to make the upload url
func prepareUploadEntriesByFolo(originalIndyURL, targetIndyURL, newBuildId string, foloRecord common.TrackedContent) map[string][]string {
	originalIndy := normIndyURL(originalIndyURL)
	targetIndy := normIndyURL(targetIndyURL)
	result := make(map[string][]string)
	for _, up := range foloRecord.Uploads {
		orgiUpUrl, targUpUrl := createUploadUrls(originalIndy, targetIndy, newBuildId, up)
		result[up.Path] = []string{up.Md5, orgiUpUrl, targUpUrl}
	}
	return result
}

func createUploadUrls(originalIndy, targetIndy, newBuildId string, up common.TrackedContentEntry) (string, string) {
	storePath := common.StoreKeyToPath(up.StoreKey) // original store, e.g, maven/hosted/build-1234
	uploadPath := path.Join("api/content", storePath, up.Path)
	orgiUpUrl := fmt.Sprintf("%s%s", originalIndy, uploadPath)                                              // original url to retrieve artifact
	alteredUploadPath := common.AlterUploadPath(up.Path, up.StoreKey, newBuildId[len(common.BUILD_TEST_):]) // replace version number
	toks := strings.Split(storePath, "/")                                                                   // get package/type, e.g., maven/hosted
	targetStorePath := path.Join(toks[0], toks[1], newBuildId, alteredUploadPath)                           // e.g, maven/hosted/build-913413/org/...
	targUpUrl := fmt.Sprintf("%sapi/folo/track/%s/%s", targetIndy, newBuildId, targetStorePath)
	return orgiUpUrl, targUpUrl
}

func normIndyURL(indyURL string) string {
	indy := indyURL
	if !strings.HasPrefix(indy, "http://") {
		indy = "http://" + indy
	}
	if !strings.HasSuffix(indy, "/") {
		indy = indy + "/"
	}
	return indy
}

func prepareDownUploadDirectories(buildId string, clearCache bool) (string, string) {
	// use "/tmp/download", which will be dropped after each run
	downloadDir := TMP_DOWNLOAD_DIR
	if !common.FileOrDirExists(downloadDir) {
		os.MkdirAll(downloadDir, os.FileMode(0755))
	}
	if !common.FileOrDirExists(downloadDir) {
		fmt.Printf("Error: cannot create directory %s for file downloading.\n", downloadDir)
		os.Exit(1)
	}

	// use ENVAR_TEST_MOUNT_PATH + "bulidId/upload" if this envar is defined
	uploadDir := TMP_UPLOAD_DIR
	envarTestMountPath := os.Getenv(common.ENVAR_TEST_MOUNT_PATH)
	if envarTestMountPath != "" {
		uploadDir = path.Join(envarTestMountPath, buildId, "upload")
		if clearCache {
			os.RemoveAll(uploadDir)
		}
	}

	if !common.FileOrDirExists(uploadDir) {
		os.MkdirAll(uploadDir, os.FileMode(0755))
	}

	if !common.FileOrDirExists(uploadDir) {
		fmt.Printf("Error: cannot create directory %s for caching uploading files.\n", uploadDir)
		os.Exit(1)
	}
	fmt.Printf("Prepared download dir: %s, upload dir: %s\n", downloadDir, uploadDir)
	return downloadDir, uploadDir
}

func setHostname(addr, hostname string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return ""
	}
	u.Host = hostname
	return u.String()
}

func delArtifact(url string) bool {
	_, _, succeeded := common.HTTPRequest(url, common.MethodDelete, nil, false, nil, nil, "", false)
	return succeeded
}
