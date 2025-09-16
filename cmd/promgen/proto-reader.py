import sys
from google.protobuf import text_format
# The following import will be based on your compiled proto file name
# For example, if your proto file is named "message.proto", it will generate "message_pb2.py"
import metrics_pb2 

def read_proto_binary(binary_file_path, proto_class):
    # Create an instance of your message class
    message = proto_class()
    
    # Read the binary file
    with open(binary_file_path, 'rb') as f:
        message.ParseFromString(f.read())
    
    # Print the message in human-readable format
    print(text_format.MessageToString(message))

def main():
    if len(sys.argv) != 2:
        print("Usage: python script.py <binary_file_path>")
        sys.exit(1)
    
    binary_file_path = sys.argv[1]
    
    try:
        # Replace YourProtoClass with the actual class name from your compiled proto
        read_proto_binary(binary_file_path, YourProtoClass)
    except Exception as e:
        print(f"Error: {e}")

if __name__ == "__main__":
    main()