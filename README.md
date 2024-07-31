Just a toy project to practice Go.

With that goal in mind, I created a website in which users could upload arbitrarily large files (capped at 64MB since I didn't want to pay for AWS storage, but I did test it with a 750MB file 😜). Their files were asynchronously encrypted by my service using the provided password. Users could then download the encrypted file (in case they wanted to have a local copy) or write down the returned UUID that could later be used to retrieve either the encrypted or the decrypted file (assuming the password for decryption matched).



With this project I put to test the concepts of concurrency in Go, including mutexes, waitgroups and goroutines. I had to decide between using channel+workers or launching goroutines for handling chunks.



The Go application developed used HTMX + Templ to provide an interface and coordinate backend calls. 

Files were always processed in chunks, minimizing the risk of OOM errors. Each chunk was individually encrypted and uploaded to S3 (in a multipart upload) using goroutines.

Adding to that, I also integrated with AWS (using EC2, VPC, ACM, S3 both for storage and pre-signed URLs to offload download) and provided a somewhat-friendly user interface (let's just say I am happy to be a backend engineer 🤣).



There are definitely things I could have done better like keeping file name and extensions to facilitate the download, implementing rate-limiting, hashing passwords for secure matching, organizing my code better, and using TDD. However, the main goal was putting my Go knowledge to the test and that I certainly did!

