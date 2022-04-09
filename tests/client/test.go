package main

func main() {
  c := make(chan any, 4)
  c <- 5
  select {
  case i := <-c:
    println(i)
  }
  close(c)
  select {
  case i := <-c:
    println(i)
  }
  select {
  case i := <-c:
    println(i)
  default:
    println("nothing")
  }
  select {
  case i := <-c:
    println(i)
  default:
    println("nothing")
  }
  select {
  case i := <-c:
    println(i)
  default:
    println("nothing")
  }
  select {
  case i := <-c:
    println(i)
  default:
    println("nothing")
  }
  println(<-c)
}
