namespace Greeter;

public static class Greeting
{
    public static string For(string name) => $"Hello, {name}!";

    public static void Main(string[] args) => Console.WriteLine(For(args.Length > 0 ? args[0] : "world"));
}
