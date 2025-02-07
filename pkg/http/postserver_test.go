package http

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakito/batch-job-controller/pkg/config"
	mock_cache "github.com/bakito/batch-job-controller/pkg/mocks/cache"
	mock_client "github.com/bakito/batch-job-controller/pkg/mocks/client"
	mock_logr "github.com/bakito/batch-job-controller/pkg/mocks/logr"
	mock_record "github.com/bakito/batch-job-controller/pkg/mocks/record"
	gm "github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	reportJSON              = `{ "test": [{ "value": 1.0, "labels": { "label_a": "AAA", "label_b": "BBB" }}] }`
	eventMessageJSON        = `{ "warning": true, "reason": "TestReason", "message": "test message" }`
	eventMessageInvalidJSON = `{ "warning": true, "reason": "testReason", "message": "test message" }`
	eventMessageArgsJSON    = `{ "warning": true, "reason": "TestReason", "message": "test message: %s" ,"args" : ["a1"]}`
)

var _ = Describe("HTTP", func() {
	var (
		mockCtrl    *gm.Controller //gomock struct
		mockLog     *mock_logr.MockLogger
		mockCache   *mock_cache.MockCache
		mockReader  *mock_client.MockReader
		executionID string
		node        string

		s   *PostServer
		cfg *config.Config

		rr     *httptest.ResponseRecorder
		router *mux.Router
		path   string
	)
	BeforeEach(func() {
		mockCtrl = gm.NewController(GinkgoT())
		mockLog = mock_logr.NewMockLogger(mockCtrl)
		mockReader = mock_client.NewMockReader(mockCtrl)
		log = mockLog
		mockCache = mock_cache.NewMockCache(mockCtrl)
		executionID = uuid.New().String()
		node = uuid.New().String()
		cfg = &config.Config{
			Metrics: config.Metrics{
				Prefix: "foo",
			},
		}

		s = &PostServer{
			ReportPath: tempDir(executionID),
		}
		s.InjectReader(mockReader)
		s.InjectCache(mockCache)
		s.InjectConfig(cfg)

		rr = httptest.NewRecorder()

		// Need to create a router that we can pass the request through so that the vars will be added to the context
		router = mux.NewRouter()
		path = fmt.Sprintf("/report/%s/%s%s", node, executionID, CallbackBaseResultSubPath)
	})
	AfterEach(func() {
		os.RemoveAll(s.ReportPath)
	})
	Context("postReport", func() {
		BeforeEach(func() {
			router.HandleFunc(CallbackBasePath+CallbackBaseResultSubPath, s.postReport)

			mockLog.EXPECT().WithValues("node", node, "id", executionID, "length", gm.Any()).Return(mockLog)
		})
		It("succeed if file is saved", func() {

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
			mockLog.EXPECT().WithValues("name", gm.Any(), "path", gm.Any()).Return(mockLog)
			mockLog.EXPECT().Info("received report")

			req, err := http.NewRequest("POST", path, strings.NewReader(reportJSON))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			Ω(rr.Code).Should(Equal(http.StatusOK))

			files, err := ioutil.ReadDir(filepath.Join(s.ReportPath, executionID))
			Ω(err).ShouldNot(HaveOccurred())
			Ω(files).Should(HaveLen(1))

			b, err := ioutil.ReadFile(filepath.Join(s.ReportPath, executionID, files[0].Name()))
			Ω(err).ShouldNot(HaveOccurred())
			Ω(b).Should(Equal([]byte(reportJSON)))
		})
		It("fails if json is invalid", func() {

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
			mockLog.EXPECT().WithValues("result", gm.Any()).Return(mockLog)
			mockLog.EXPECT().Error(gm.Any(), gm.Any())

			req, err := http.NewRequest("POST", path, strings.NewReader("foo"))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			Ω(rr.Code).Should(Equal(http.StatusBadRequest))

			files, err := ioutil.ReadDir(filepath.Join(s.ReportPath, executionID))
			Ω(err).ShouldNot(HaveOccurred())
			Ω(files).Should(HaveLen(0))
		})
	})

	Context("middleware", func() {
		var (
			handler *testing.FakeHandler
		)
		BeforeEach(func() {
			handler = &testing.FakeHandler{}
			h := s.middleware(handler)
			router.HandleFunc(CallbackBasePath+CallbackBaseResultSubPath, h.ServeHTTP)
		})

		It("should allow the request", func() {
			mockCache.EXPECT().Has(node, executionID).Return(true)

			req, err := http.NewRequest("POST", path, strings.NewReader(""))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			handler.ValidateRequestCount(GinkgoT(), 1)
		})
		It("should allow the request if cache is nil", func() {
			mockCache.EXPECT().Has(node, executionID).Return(true)
			s.InjectCache(nil)
			req, err := http.NewRequest("POST", path, strings.NewReader(""))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			handler.ValidateRequestCount(GinkgoT(), 1)
		})
		It("should deny if execution is not known", func() {
			mockCache.EXPECT().Has(node, executionID).Return(false)

			req, err := http.NewRequest("POST", path, strings.NewReader(""))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			Ω(rr.Code).Should(Equal(http.StatusNotAcceptable))
			Ω(rr.Body.String()).Should(HavePrefix(errorMiddlewareNotAcceptable))
			handler.ValidateRequestCount(GinkgoT(), 0)
		})
	})

	Context("postFile", func() {
		var (
			path                   string
			fileName               string
			generatedFileExtension string
		)
		BeforeEach(func() {
			fileName = uuid.New().String() + ".txt"
			path = fmt.Sprintf("/report/%s/%s%s", node, executionID, CallbackBaseFileSubPath)
			router.HandleFunc(CallbackBasePath+CallbackBaseFileSubPath, s.postFile)

			mockLog.EXPECT().WithValues("node", node, "id", executionID, "name", gm.Any(), "path", gm.Any(), "length", gm.Any()).Return(mockLog)

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
			mockLog.EXPECT().WithValues("name", gm.Any(), "path", gm.Any()).Return(mockLog)
			mockLog.EXPECT().Info("received file")
		})
		AfterEach(func() {
			Ω(rr.Code).Should(Equal(http.StatusOK))

			files, err := ioutil.ReadDir(filepath.Join(s.ReportPath, executionID))
			Ω(err).ShouldNot(HaveOccurred())
			Ω(files).Should(HaveLen(1))
			if generatedFileExtension != "" {
				Ω(files[0].Name()).Should(HavePrefix(node + "-"))
				Ω(files[0].Name()).Should(HaveSuffix(generatedFileExtension))
			} else {
				Ω(files[0].Name()).Should(Equal(node + "-" + fileName))
			}

			b, err := ioutil.ReadFile(filepath.Join(s.ReportPath, executionID, files[0].Name()))
			Ω(err).ShouldNot(HaveOccurred())
			Ω(b).Should(Equal([]byte("foo")))
		})
		It("succeed if file is saved with correct name from query parameter", func() {
			req, err := http.NewRequest("POST", fmt.Sprintf("%s?name=%s", path, fileName), strings.NewReader("foo"))
			Ω(err).ShouldNot(HaveOccurred())
			router.ServeHTTP(rr, req)
		})
		It("succeed if file is saved with correct name from header", func() {
			req, err := http.NewRequest("POST", path, strings.NewReader("foo"))
			req.Header.Add("Content-Disposition", fmt.Sprintf(`attachment;filename="%s"`, fileName))
			Ω(err).ShouldNot(HaveOccurred())
			router.ServeHTTP(rr, req)
		})
		It("succeed if file is saved with generated name with .file extension", func() {
			generatedFileExtension = ".file"
			req, err := http.NewRequest("POST", path, strings.NewReader("foo"))
			Ω(err).ShouldNot(HaveOccurred())
			router.ServeHTTP(rr, req)
		})
		It("succeed if file is saved with generated name with .txt extension", func() {
			generatedFileExtension = ".txt"
			req, err := http.NewRequest("POST", path, strings.NewReader("foo"))
			req.Header.Add("Content-Type", "text/plain")
			Ω(err).ShouldNot(HaveOccurred())
			router.ServeHTTP(rr, req)
		})
		It("succeed if file is saved with generated name with .json extension", func() {
			generatedFileExtension = ".json"
			req, err := http.NewRequest("POST", path, strings.NewReader("foo"))
			req.Header.Add("content-type", "application/json")
			Ω(err).ShouldNot(HaveOccurred())
			router.ServeHTTP(rr, req)
		})

	})
	Context("postEvent", func() {
		var (
			path       string
			mockRecord *mock_record.MockEventRecorder
		)
		BeforeEach(func() {
			mockRecord = mock_record.NewMockEventRecorder(mockCtrl)
			s.InjectEventRecorder(mockRecord)
			path = fmt.Sprintf("/report/%s/%s%s", node, executionID, CallbackBaseEventSubPath)
			router.HandleFunc(CallbackBasePath+CallbackBaseEventSubPath, s.postEvent)

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
		})
		It("succeed if event with message is sent", func() {

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
			mockLog.EXPECT().WithValues("node", node, "id", executionID, "length", gm.Any()).Return(mockLog)
			mockRecord.EXPECT().Event(gm.Any(), "Warning", "TestReason", "test message")
			mockLog.EXPECT().Info("event created")
			mockReader.EXPECT().
				Get(gm.Any(), client.ObjectKey{Namespace: s.Config.Namespace, Name: s.Config.PodName(node, executionID)}, gm.AssignableToTypeOf(&corev1.Pod{}))

			req, err := http.NewRequest("POST", path, strings.NewReader(eventMessageJSON))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			Ω(rr.Code).Should(Equal(http.StatusOK))
		})
		It("succeed if event with message with args is sent", func() {

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
			mockLog.EXPECT().WithValues("node", node, "id", executionID, "length", gm.Any()).Return(mockLog)
			mockRecord.EXPECT().Eventf(gm.Any(), "Warning", "TestReason", "test message: %s", "a1")
			mockLog.EXPECT().Info("event created")
			mockReader.EXPECT().
				Get(gm.Any(), client.ObjectKey{Namespace: s.Config.Namespace, Name: s.Config.PodName(node, executionID)}, gm.AssignableToTypeOf(&corev1.Pod{}))

			req, err := http.NewRequest("POST", path, strings.NewReader(eventMessageArgsJSON))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			Ω(rr.Code).Should(Equal(http.StatusOK))
		})

		It("fails if json is invalid", func() {

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
			mockLog.EXPECT().WithValues("node", node, "id", executionID, "length", gm.Any()).Return(mockLog)
			mockLog.EXPECT().WithValues("result", gm.Any()).Return(mockLog)
			mockLog.EXPECT().Error(gm.Any(), gm.Any())

			req, err := http.NewRequest("POST", path, strings.NewReader("foo"))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			Ω(rr.Code).Should(Equal(http.StatusBadRequest))
			Ω(rr.Body.String()).Should(HavePrefix("error decoding event"))
		})

		It("fails if event is invalid", func() {

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
			mockLog.EXPECT().WithValues("node", node, "id", executionID, "length", gm.Any()).Return(mockLog)
			mockLog.EXPECT().WithValues("result", gm.Any()).Return(mockLog)
			mockLog.EXPECT().Error(gm.Any(), gm.Any())

			req, err := http.NewRequest("POST", path, strings.NewReader(eventMessageInvalidJSON))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			Ω(rr.Code).Should(Equal(http.StatusBadRequest))
			Ω(rr.Body.String()).Should(ContainSubstring("'Reason' failed on the 'first_char_must_be_uppercase' tag"))
		})

		It("fails if pod not found", func() {

			mockCache.EXPECT().ReportReceived(executionID, node, gm.Any(), gm.Any())
			mockLog.EXPECT().WithValues("node", node, "id", executionID, "length", gm.Any()).Return(mockLog)
			mockLog.EXPECT().WithValues("result", gm.Any()).Return(mockLog)
			mockLog.EXPECT().Error(gm.Any(), gm.Any())
			mockReader.EXPECT().
				Get(gm.Any(), client.ObjectKey{Namespace: s.Config.Namespace, Name: s.Config.PodName(node, executionID)}, gm.AssignableToTypeOf(&corev1.Pod{})).
				Return(fmt.Errorf("error"))

			req, err := http.NewRequest("POST", path, strings.NewReader(eventMessageArgsJSON))
			Ω(err).ShouldNot(HaveOccurred())

			router.ServeHTTP(rr, req)

			Ω(rr.Code).Should(Equal(http.StatusNotFound))
			Ω(strings.TrimSpace(rr.Body.String())).Should(HavePrefix("error finding pod"))
		})
	})

	Context("StaticFileServer", func() {
		It("returns a file server", func() {
			sfs := StaticFileServer(1234, "path")
			Ω(sfs).ShouldNot(BeNil())
			Ω(sfs.(*Server).Port).Should(Equal(1234))
			Ω(sfs.(*Server).Kind).Should(Equal("public"))
			Ω(sfs.(*Server).Handler).ShouldNot(BeNil())
		})
	})

	Context("GenericAPIServer", func() {
		BeforeEach(func() {
			mockLog.EXPECT().Info(gm.Any(), gm.Any(), gm.Any(), gm.Any(), gm.Any(), gm.Any(), gm.Any())
		})
		It("returns a server", func() {
			sfs := GenericAPIServer(1234, "")
			Ω(sfs).ShouldNot(BeNil())
			Ω(sfs.(*PostServer).Port).Should(Equal(1234))
			Ω(sfs.(*PostServer).Kind).Should(Equal("internal"))
		})
	})
})

func tempDir(id string) string {
	dir, err := ioutil.TempDir("", "go-test-")
	Ω(err).ShouldNot(HaveOccurred())
	err = os.MkdirAll(filepath.Join(dir, id), os.ModePerm)
	Ω(err).ShouldNot(HaveOccurred())
	return dir
}
