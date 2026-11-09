package main
import("os";"time";"strings")
func main(){
 if len(os.Args)!=1{panic("no arguments expected")}
 data,err:=os.ReadFile("wordpress.txt");if err!=nil{panic(err)}
 f,err:=os.Create("results.txt");if err!=nil{panic(err)}
 if _,err=f.Write(data);err!=nil{panic(err)};if err=f.Sync();err!=nil{panic(err)};f.Close()
 if strings.Contains(string(data),":recovery"){time.Sleep(2*time.Second)}else{time.Sleep(75*time.Second)}
}
