namespace Greeter.Tests;

public class GreetingTests
{
    [Fact]
    public void GreetsByName() => Assert.Equal("Hello, Ada!", Greeting.For("Ada"));

    [Theory]
    [InlineData("x")]
    [InlineData("y")]
    public void EndsWithBang(string name) => Assert.EndsWith("!", Greeting.For(name));

    [Fact(Skip = "kept to show a skipped test")]
    public void Skipped() { }
}
