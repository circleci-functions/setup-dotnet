namespace Greeter.MSTests;

[TestClass]
public sealed class GreetingTests
{
    [TestMethod]
    public void StartsWithHello() => Assert.StartsWith("Hello", Greeting.For("Ada"));
}
