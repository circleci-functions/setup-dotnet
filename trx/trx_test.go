package trx_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"

	"github.com/circleci-functions/setup-dotnet/trx"
)

func TestConvert(t *testing.T) {
	for _, fixture := range []string{"xunit", "nunit", "mstest", "mstest-mtp"} {
		t.Run(fixture, func(t *testing.T) {
			framework, _, _ := strings.Cut(fixture, "-")
			f, err := os.Open(filepath.Join("testdata", fixture+".trx"))
			assert.NilError(t, err)
			defer f.Close()

			suite, err := trx.Convert(f, "", "Sample."+framework+".Tests/Sample."+framework+".Tests.csproj")
			assert.NilError(t, err)
			assert.Equal(t, suite.Tests, 5)
			assert.Equal(t, suite.Failures, 1)
			assert.Equal(t, suite.Skipped, 1)
			for _, c := range suite.Cases {
				assert.Equal(t, c.ClassName, "Sample."+framework+".Tests.CalculatorTests")
				assert.Assert(t, !strings.HasPrefix(c.Name, c.ClassName), c.Name)
			}

			var buf bytes.Buffer
			assert.NilError(t, trx.Write(&buf, suite))
			assert.Equal(t, suite.Name, "Sample."+framework+".Tests")
			golden.Assert(t, buf.String(), fixture+".junit.xml")
		})
	}
}

func TestConvert_DataDrivenRows(t *testing.T) {
	const doc = `<TestRun xmlns="http://microsoft.com/schemas/VisualStudio/TeamTest/2010">
  <Results>
    <UnitTestResult testId="t1" testName="Rows" duration="00:00:01.5000000" outcome="Failed">
      <InnerResults>
        <UnitTestResult testName="Rows (1)" duration="00:00:00.5000000" outcome="Passed" />
        <UnitTestResult testName="Rows (2)" duration="00:00:01.0000000" outcome="Failed" />
      </InnerResults>
    </UnitTestResult>
  </Results>
  <TestDefinitions>
    <UnitTest id="t1"><TestMethod className="Ns.Data" name="Rows" /></UnitTest>
  </TestDefinitions>
</TestRun>`
	suite, err := trx.Convert(strings.NewReader(doc), "s", "")
	assert.NilError(t, err)
	assert.Equal(t, suite.Tests, 2)
	assert.Equal(t, suite.Failures, 1)
	assert.Equal(t, suite.Time, "1.500")
	assert.Equal(t, suite.Cases[1].ClassName, "Ns.Data")
	assert.Equal(t, suite.Cases[1].Name, "Rows (2)")
}

func TestConvert_Invalid(t *testing.T) {
	_, err := trx.Convert(strings.NewReader("not xml"), "s", "")
	assert.ErrorContains(t, err, "parse TRX")
}
