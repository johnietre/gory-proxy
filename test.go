package main

// import (
// )

const tests = `
hello
goodbye
` + "goodbye\nmroning"

func main() {
	println(tests[0] == '\n')
	println(tests)
}
